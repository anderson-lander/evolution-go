// Package redact tira segredos do texto que vai para o log.
//
// Lander, 0.7.2-lander.2 (12/11/2026). A 0.7.2 escrevia em texto aberto:
//   - o token da instância ("Attempting to update instance in DB: %+v");
//   - o endereço do webhook com o segredo do CRM no caminho ("webhook failed -
//     url: ..."), a cada evento e a cada tentativa;
//   - códigos e tokens que chegam no caminho ou na query (log de acesso do Gin:
//     /license/activate?code=..., /passkey-ceremony/:token);
//   - a URL assinada do MinIO (que é uma credencial temporária).
//
// Tudo isso ia para o console (no Render, o log do serviço) e para o arquivo de
// log por instância, que a própria API devolve em /instance/logs. Mascarar na
// SAÍDA cobre as dezenas de chamadas que já existem sem mexer em cada uma, e
// nada do que a API devolve, envia ou grava no banco muda.
package redact

import (
	"regexp"
	"strings"
)

// Mask é o que fica no lugar do segredo.
const Mask = "***"

var (
	// "chave: valor" e "chave=valor": o %+v de struct (Token:abc), JSON
	// ("token":"abc") e query solta. O nome pode vir colado (webhookToken,
	// MINIO_SECRET_KEY).
	campo = regexp.MustCompile(`(?i)("?\b\w*(?:token|apikey|api_key|api-key|password|passwd|secret)\w*"?\s*[:=]\s*"?)([^\s,;}"&)\]]+)`)

	// "Bearer abc".
	portador = regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=-]+`)

	// Endereço com esquema (http, https, ws, amqp, postgres...).
	link = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>]+`)

	// Id de instância: não é credencial e ajuda a investigar. Nenhum segredo
	// da Evolution tem esse formato (o da passkey é hexadecimal sem hífen; o
	// do webhook do CRM é base64 de URL).
	uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// Text mascara os segredos de uma linha de log.
func Text(s string) string {
	if s == "" {
		return s
	}
	s = link.ReplaceAllStringFunc(s, limparLink)
	s = portador.ReplaceAllString(s, "${1}"+Mask)
	s = campo.ReplaceAllString(s, "${1}"+Mask)
	return s
}

// Secret mostra só se havia um valor, nunca o valor.
func Secret(s string) string {
	if s == "" {
		return `""`
	}
	return Mask
}

// Path mascara, num caminho com query ("/a/b?c=d#e"), o que parece segredo:
// segmento longo com dígito (ou longo demais para ser palavra), todo valor de
// query e o fragmento. Id de instância (UUID) continua visível.
func Path(s string) string {
	caminho, query, fragmento := s, "", ""
	if i := strings.IndexByte(caminho, '#'); i >= 0 {
		caminho, fragmento = caminho[:i], "#"+Mask
	}
	temQuery := false
	if i := strings.IndexByte(caminho, '?'); i >= 0 {
		caminho, query, temQuery = caminho[:i], caminho[i+1:], true
	}
	partes := strings.Split(caminho, "/")
	for i, p := range partes {
		if pareceSegredo(p) {
			partes[i] = Mask
		}
	}
	saida := strings.Join(partes, "/")
	if temQuery {
		pares := strings.Split(query, "&")
		for i, par := range pares {
			if chave, valor, ok := strings.Cut(par, "="); ok && valor != "" {
				pares[i] = chave + "=" + Mask
			} else if !ok && pareceSegredo(par) {
				pares[i] = Mask
			}
		}
		saida += "?" + strings.Join(pares, "&")
	}
	return saida + fragmento
}

func pareceSegredo(p string) bool {
	if uuidRe.MatchString(p) {
		return false
	}
	if len(p) >= 32 {
		return true
	}
	return len(p) >= 16 && strings.ContainsAny(p, "0123456789")
}

func limparLink(s string) string {
	// Pontuação colada no fim ("url: http://x/y, attempt: 1") não é do endereço.
	fim := len(s)
	for fim > 0 && strings.IndexByte(".,;:)]}!?", s[fim-1]) >= 0 {
		fim--
	}
	corpo, cauda := s[:fim], s[fim:]
	i := strings.Index(corpo, "://")
	if i < 0 {
		return s
	}
	esquema, resto := corpo[:i+3], corpo[i+3:]
	host, caminho := resto, ""
	if j := strings.IndexAny(resto, "/?#"); j >= 0 {
		host, caminho = resto[:j], resto[j:]
	}
	// usuário:senha@host (postgres://, amqp://)
	if at := strings.LastIndexByte(host, '@'); at >= 0 {
		host = Mask + "@" + host[at+1:]
	}
	return esquema + host + Path(caminho) + cauda
}
