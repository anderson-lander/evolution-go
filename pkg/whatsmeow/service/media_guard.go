package whatsmeow_service

import (
	"strings"

	"github.com/evolution-foundation/evolution-go/pkg/config"
	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	"github.com/evolution-foundation/evolution-go/pkg/internal/event_types"
	"github.com/evolution-foundation/evolution-go/pkg/utils"
)

// Por que isto existe: com WEBHOOK_FILES ligado, toda mídia que chega (ou que a instância manda)
// era baixada e convertida em base64 ANTES de se saber se alguém ia receber o evento. Uma
// instância que assina só CONNECTION recebeu um vídeo de 73 MB numa conversa privada, montou
// 102 MB de base64 que ninguém ia ler, e o servidor (512 MB) morreu por memória, derrubando
// todas as instâncias junto. As duas regras abaixo cortam o download nesses casos; a mensagem
// segue para o webhook do mesmo jeito, só sem o arquivo (com a mediaKey/directPath de sempre,
// para quem quiser pedir depois por /message/downloadmedia).

// ParseSubscriptions lê instance.Events do mesmo jeito que CallWebhook sempre leu.
// discarded traz o que não é tipo de evento conhecido (CallWebhook registra isso no log).
func ParseSubscriptions(events string) (subscriptions []string, discarded []string) {
	eventArray := strings.Split(events, ",")

	if len(eventArray) < 1 {
		return []string{event_types.MESSAGE, event_types.SEND_MESSAGE}, nil
	}

	for _, arg := range eventArray {
		if !event_types.IsEventType(arg) {
			discarded = append(discarded, arg)
			continue
		}
		if !utils.Find(subscriptions, arg) {
			subscriptions = append(subscriptions, arg)
		}
	}
	return subscriptions, discarded
}

// EventReachesSubscriber diz se CallWebhook entregaria um evento "Message" ou "SendMessage" desta
// conversa a alguma assinatura da instância. Espelha as regras dos dois cases de CallWebhook.
// Para qualquer outro tipo de evento devolve true (não é o caso que este guarda cobre).
func EventReachesSubscriber(instance *instance_model.Instance, eventType, chat string) bool {
	if instance == nil {
		return true
	}

	var direct string
	switch eventType {
	case "Message":
		direct = event_types.MESSAGE
	case "SendMessage":
		direct = event_types.SEND_MESSAGE
	default:
		return true
	}

	subscriptions, _ := ParseSubscriptions(instance.Events)
	if contains(subscriptions, event_types.ALL) || contains(subscriptions, direct) {
		return true
	}
	if strings.HasSuffix(chat, "@g.us") && contains(subscriptions, event_types.GROUP) {
		return true
	}
	if strings.HasSuffix(chat, "@newsletter") && contains(subscriptions, event_types.NEWSLETTER) {
		return true
	}
	return false
}

// MediaSkipReason devolve por que NÃO baixar a mídia de um evento, ou "" quando é para baixar.
// declaredSize é o FileLength que veio na própria mensagem (0 quando não veio).
func MediaSkipReason(cfg *config.Config, instance *instance_model.Instance, eventType, chat string, declaredSize uint64) string {
	if cfg != nil && cfg.WebhookFilesMaxBytes > 0 && declaredSize > uint64(cfg.WebhookFilesMaxBytes) {
		return "maior que WEBHOOK_FILES_MAX_MB"
	}

	// Fila global (AMQP/NATS) recebe todo evento, assinado ou não: aí alguém lê o arquivo.
	if cfg != nil && (cfg.AmqpGlobalEnabled || cfg.NatsGlobalEnabled) {
		return ""
	}

	if !EventReachesSubscriber(instance, eventType, chat) {
		return "nenhuma assinatura recebe este evento"
	}
	return ""
}

// MediaTooLarge cobre a mídia que veio sem FileLength: o tamanho só se sabe depois de baixar.
func MediaTooLarge(cfg *config.Config, size int) bool {
	return cfg != nil && cfg.WebhookFilesMaxBytes > 0 && int64(size) > cfg.WebhookFilesMaxBytes
}
