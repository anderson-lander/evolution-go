package whatsmeow_service

import (
	"reflect"
	"testing"

	"github.com/evolution-foundation/evolution-go/pkg/config"
	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
)

const mb = 1024 * 1024

func TestParseSubscriptions(t *testing.T) {
	subs, discarded := ParseSubscriptions("MESSAGE,SEND_MESSAGE,MESSAGE,bogus,CONNECTION")
	if want := []string{"MESSAGE", "SEND_MESSAGE", "CONNECTION"}; !reflect.DeepEqual(subs, want) {
		t.Fatalf("subscriptions = %v, want %v", subs, want)
	}
	if want := []string{"bogus"}; !reflect.DeepEqual(discarded, want) {
		t.Fatalf("discarded = %v, want %v", discarded, want)
	}

	// Events vazio: CallWebhook nunca entregou nada nesse caso, e continua assim.
	subs, discarded = ParseSubscriptions("")
	if len(subs) != 0 || !reflect.DeepEqual(discarded, []string{""}) {
		t.Fatalf("empty events: subs=%v discarded=%v", subs, discarded)
	}
}

func TestEventReachesSubscriber(t *testing.T) {
	const privado = "554799999999@s.whatsapp.net"
	const grupo = "120363431218390831@g.us"
	const canal = "123@newsletter"

	cases := []struct {
		name   string
		events string
		event  string
		chat   string
		want   bool
	}{
		// o caso de 09/11/26: número que só assina conexão recebeu vídeo numa conversa privada
		{"so CONNECTION, privado", "CONNECTION", "Message", privado, false},
		{"so CONNECTION, grupo", "CONNECTION", "Message", grupo, false},
		{"so CONNECTION, envio", "CONNECTION", "SendMessage", grupo, false},
		{"MESSAGE recebe Message", "MESSAGE,CONNECTION", "Message", privado, true},
		{"MESSAGE nao recebe SendMessage", "MESSAGE,CONNECTION", "SendMessage", privado, false},
		{"SEND_MESSAGE recebe SendMessage", "SEND_MESSAGE", "SendMessage", privado, true},
		{"ALL recebe tudo", "ALL", "Message", privado, true},
		{"GROUP recebe Message de grupo", "GROUP,CONNECTION", "Message", grupo, true},
		{"GROUP nao recebe privado", "GROUP,CONNECTION", "Message", privado, false},
		{"GROUP recebe SendMessage de grupo", "GROUP", "SendMessage", grupo, true},
		{"NEWSLETTER recebe canal", "NEWSLETTER", "Message", canal, true},
		{"minusculo nao e tipo valido", "message", "Message", privado, false},
		{"outro evento passa direto", "CONNECTION", "Receipt", privado, true},
	}

	for _, c := range cases {
		inst := &instance_model.Instance{Events: c.events}
		if got := EventReachesSubscriber(inst, c.event, c.chat); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}

	if !EventReachesSubscriber(nil, "Message", privado) {
		t.Error("instância nil deve manter o comportamento antigo (baixar)")
	}
}

func TestMediaSkipReason(t *testing.T) {
	cfg := &config.Config{WebhookFilesMaxBytes: 25 * mb}
	vendedor := &instance_model.Instance{Events: "MESSAGE,SEND_MESSAGE,READ_RECEIPT,CONNECTION"}
	dono := &instance_model.Instance{Events: "CONNECTION"}
	const privado = "554799999999@s.whatsapp.net"

	if r := MediaSkipReason(cfg, vendedor, "Message", privado, 5*mb); r != "" {
		t.Errorf("vídeo pequeno no vendedor deveria baixar, veio %q", r)
	}
	if r := MediaSkipReason(cfg, vendedor, "Message", privado, 0); r != "" {
		t.Errorf("sem FileLength deveria baixar (o teto confere depois), veio %q", r)
	}
	if r := MediaSkipReason(cfg, vendedor, "Message", privado, 76733842); r == "" {
		t.Error("o vídeo de 73 MB no vendedor não deveria baixar")
	}
	if r := MediaSkipReason(cfg, vendedor, "Message", privado, 25*mb); r != "" {
		t.Errorf("exatamente no teto ainda baixa, veio %q", r)
	}
	if r := MediaSkipReason(cfg, dono, "Message", privado, 1*mb); r == "" {
		t.Error("instância só CONNECTION não deveria baixar nem foto pequena")
	}

	semTeto := &config.Config{WebhookFilesMaxBytes: 0}
	if r := MediaSkipReason(semTeto, vendedor, "Message", privado, 76733842); r != "" {
		t.Errorf("teto 0 = sem limite, veio %q", r)
	}

	filaGlobal := &config.Config{WebhookFilesMaxBytes: 25 * mb, NatsGlobalEnabled: true}
	if r := MediaSkipReason(filaGlobal, dono, "Message", privado, 1*mb); r != "" {
		t.Errorf("com fila global alguém lê o arquivo, veio %q", r)
	}
	if r := MediaSkipReason(filaGlobal, dono, "Message", privado, 76733842); r == "" {
		t.Error("o teto vale mesmo com fila global")
	}
}

func TestMediaTooLarge(t *testing.T) {
	cfg := &config.Config{WebhookFilesMaxBytes: 25 * mb}
	if MediaTooLarge(cfg, 25*mb) || !MediaTooLarge(cfg, 25*mb+1) {
		t.Error("teto de 25 MB errado")
	}
	if MediaTooLarge(&config.Config{}, 500*mb) || MediaTooLarge(nil, 500*mb) {
		t.Error("sem teto configurado nada é grande demais")
	}
}
