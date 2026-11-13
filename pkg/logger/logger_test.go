package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evolution-foundation/evolution-go/pkg/config"
)

// Lander, 0.7.2-lander.2: o arquivo da instância (o que /instance/logs devolve)
// sai sem o token da instância nem o segredo do webhook.
func TestArquivoDaInstanciaSaiSemSegredo(t *testing.T) {
	dir := t.TempDir()
	lm := NewLoggerManager(&config.Config{LogDirectory: dir, LogMaxSize: 1, LogMaxBackups: 1, LogMaxAge: 1})
	l := lm.GetLogger("inst-1")
	defer l.Close()

	const tokenInstancia = "621d0c9250344f0b89d6f698cd00e0b4da48226a"
	const tokenWebhook = "ay7fnxnngheVQ3ADjE8bouixe7LeokFQ_CuuQKgUS24"
	l.LogInfo("[%s] Attempting to update instance in DB: %+v", "inst-1", struct {
		Name    string
		Token   string
		Webhook string
	}{"evo-teste", tokenInstancia, "http://h:5000/whatsapp/webhook/evolution/" + tokenWebhook})
	l.LogWarn("[%s] webhook failed - url: %s, attempt: %d", "inst-1",
		"http://h:5000/whatsapp/webhook/evolution/"+tokenWebhook, 1)

	bruto, err := os.ReadFile(filepath.Join(dir, "inst-1", "instance.log"))
	if err != nil {
		t.Fatal(err)
	}
	texto := string(bruto)
	for _, segredo := range []string{tokenInstancia, tokenWebhook} {
		if strings.Contains(texto, segredo) {
			t.Fatalf("segredo no arquivo de log: %s", texto)
		}
	}
	for _, fica := range []string{"Name:evo-teste", "Token:***", "webhook/evolution/***", "attempt: 1"} {
		if !strings.Contains(texto, fica) {
			t.Fatalf("faltou %q no log: %s", fica, texto)
		}
	}
}
