package redact

import (
	"strings"
	"testing"
)

const (
	tokenInstancia = "621d0c9250344f0b89d6f698cd00e0b4da48226a"
	tokenWebhook   = "ay7fnxnngheVQ3ADjE8bouixe7LeokFQ_CuuQKgUS24"
	idInstancia    = "3cee62cf-6266-4871-8a73-edd4fb531022"
)

func semSegredo(t *testing.T, saida string, segredos ...string) {
	t.Helper()
	for _, s := range segredos {
		if strings.Contains(saida, s) {
			t.Fatalf("segredo %q ficou no log: %s", s, saida)
		}
	}
}

// A linha real da 0.7.2 que levou os dois tokens ao log (12/11/2026).
func TestStructDaInstanciaNaoLevaTokenNemWebhook(t *testing.T) {
	linha := "[" + idInstancia + "] Attempting to update instance in DB: &{Id:" + idInstancia +
		" Name:evo-conta-teste Token:" + tokenInstancia +
		" Webhook:http://127.0.0.1:5000/whatsapp/webhook/evolution/" + tokenWebhook +
		" RabbitmqEnable: Jid:5500000000000:88@s.whatsapp.net Connected:true}"
	saida := Text(linha)
	semSegredo(t, saida, tokenInstancia, tokenWebhook)
	for _, fica := range []string{idInstancia, "Name:evo-conta-teste", "Token:***",
		"http://127.0.0.1:5000/whatsapp/webhook/evolution/***", "Connected:true"} {
		if !strings.Contains(saida, fica) {
			t.Fatalf("sumiu %q: %s", fica, saida)
		}
	}
}

func TestWebhookFalhouMantemPontuacaoEOErro(t *testing.T) {
	linha := `[x] webhook failed - url: http://host.docker.internal:5000/whatsapp/webhook/evolution/` + tokenWebhook +
		`, attempt: 1, error: Post "http://host.docker.internal:5000/whatsapp/webhook/evolution/` + tokenWebhook +
		`": dial tcp 127.0.0.1:5000: connect: connection refused`
	saida := Text(linha)
	semSegredo(t, saida, tokenWebhook)
	if !strings.Contains(saida, "evolution/***, attempt: 1") || !strings.Contains(saida, `evolution/***": dial tcp`) {
		t.Fatalf("pontuação ou erro perdidos: %s", saida)
	}
}

func TestQueryUsuarioSenhaEFragmento(t *testing.T) {
	saida := Text("GET https://min.io/b/f.png?X-Amz-Signature=abc123&X-Amz-Credential=key%2Fx " +
		"postgres://evogo:senha-do-banco@evo-db:5432/evogo_auth?sslmode=disable http://x/y#seg")
	semSegredo(t, saida, "abc123", "key%2Fx", "senha-do-banco", "#seg")
	for _, fica := range []string{"X-Amz-Signature=***", "postgres://***@evo-db:5432/evogo_auth?sslmode=***", "http://x/y#***"} {
		if !strings.Contains(saida, fica) {
			t.Fatalf("faltou %q: %s", fica, saida)
		}
	}
}

func TestCampoChaveValorEBearer(t *testing.T) {
	casos := map[string]string{
		`{"token":"abc-def-ghi","name":"x"}`: `{"token":"***","name":"x"}`,
		`apikey: s3cr3t`:                     `apikey: ***`,
		`MINIO_SECRET_KEY=valor`:             `MINIO_SECRET_KEY=***`,
		`webhookToken=xyz`:                   `webhookToken=***`,
		`Authorization: Bearer eyJ.abc.def`:  `Authorization: Bearer ***`,
	}
	for entrada, esperado := range casos {
		if saida := Text(entrada); saida != esperado {
			t.Errorf("Text(%q) = %q, esperado %q", entrada, saida, esperado)
		}
	}
}

func TestTextoSemSegredoNaoMuda(t *testing.T) {
	for _, linha := range []string{
		"[" + idInstancia + "] QR code generated #1 (max: 5)",
		"[" + idInstancia + "] Handshake version set to 2.3000.1052556849 (library default was 2.3000.1042386815)",
		"events.Connected to Whatsapp for user ''",
		"405: client outdated (WhatsApp rejected client version 2.3000.1042386815)",
		"",
	} {
		if saida := Text(linha); saida != linha {
			t.Errorf("mudou sem segredo:\n  %q\n  %q", linha, saida)
		}
	}
}

func TestPathDoLogDeAcesso(t *testing.T) {
	casos := map[string]string{
		"/license/activate?code=abc123":                      "/license/activate?code=***",
		"/passkey-ceremony/0123456789abcdef0123456789abcdef": "/passkey-ceremony/***",
		"/instance/info/" + idInstancia:                      "/instance/info/" + idInstancia,
		"/instance/all":                                      "/instance/all",
		"/send/text":                                         "/send/text",
		"/manager/license/callback":                          "/manager/license/callback",
	}
	for entrada, esperado := range casos {
		if saida := Path(entrada); saida != esperado {
			t.Errorf("Path(%q) = %q, esperado %q", entrada, saida, esperado)
		}
	}
}

func TestSecret(t *testing.T) {
	if Secret("") != `""` || Secret("qualquer") != Mask {
		t.Fatal("Secret deveria dizer só se havia valor")
	}
}
