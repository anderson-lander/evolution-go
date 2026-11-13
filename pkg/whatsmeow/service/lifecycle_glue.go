package whatsmeow_service

// Lander patch (2026-11-12): glue between the service and pkg/whatsmeow/lifecycle.
// See the package comment of lifecycle for the incident that motivated it.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"

	logger_wrapper "github.com/evolution-foundation/evolution-go/pkg/logger"
	"github.com/evolution-foundation/evolution-go/pkg/whatsmeow/lifecycle"
)

// OutdatedDisconnectReason is what goes to instances.disconnect_reason after a
// 405. It starts with the code on purpose: integrations (the Lander CRM among
// them) read the leading "405" as "client outdated", the same grammar as
// whatsmeow's ConnectFailureReason.String().
func OutdatedDisconnectReason(version string) string {
	return fmt.Sprintf("405: client outdated (WhatsApp rejected client version %s)", version)
}

// ErrClientOutdated is returned by automatic paths during the 405 cooldown.
var ErrClientOutdated = errors.New("whatsapp client outdated (405): automatic reconnect disabled, the server must be updated")

// maskToken keeps tokens out of log files (the logs are readable through
// GET /instance/logs/:id).
func maskToken(token string) string {
	if len(token) <= 8 {
		return "***"
	}
	return token[:4] + "…" + token[len(token)-2:]
}

// DiscardClient removes a client from memory without touching the pairing
// stored in the auth database. Exposed on the interface for instance_service.
func (w whatsmeowService) DiscardClient(instanceId string, reason string) {
	w.discardClient(instanceId, nil, reason)
}

// discardClient: event handler removed, socket closed, maps cleaned only when
// they still point to THIS client, database pool closed. The goroutine that
// was running StartClient's loop for this client notices it is no longer the
// registered one and returns (see the ownership check in StartClient).
//
// Never call it synchronously from inside an event handler of the same
// client: RemoveEventHandler needs the lock that the dispatcher is holding.
func (w whatsmeowService) discardClient(instanceId string, client *whatsmeow.Client, reason string) {
	logger := w.loggerWrapper.GetLogger(instanceId)
	if client == nil {
		client = w.clientPointer[instanceId]
	}
	if client == nil {
		return
	}
	if mycli, ok := w.myClientPointer[instanceId]; ok && mycli.WAClient == client {
		if mycli.eventHandlerID != 0 {
			client.RemoveEventHandler(mycli.eventHandlerID)
		}
		delete(w.myClientPointer, instanceId)
	}
	client.Disconnect()
	if w.clientPointer[instanceId] == client {
		delete(w.clientPointer, instanceId)
	}
	if err := lifecycle.Default.CloseFor(instanceId, client); err != nil {
		logger.LogWarn("[%s] Error closing the database pool of a discarded client: %v", instanceId, err)
	}
	if lifecycle.Default.Get(instanceId).Phase != lifecycle.PhaseOutdated {
		lifecycle.Default.Set(instanceId, lifecycle.PhaseStopped, "")
	}
	logger.LogInfo("[%s] Client discarded from memory (%s); pairing data kept", instanceId, reason)
}

// failStart is the single exit for a StartClient whose Connect() failed. It
// replaces the original bare `return`, which left a dead client registered.
func (w whatsmeowService) failStart(cd *ClientData, client *whatsmeow.Client, err error) {
	id := cd.Instance.Id
	logger := w.loggerWrapper.GetLogger(id)
	msg := "connect failed"
	if err != nil {
		msg = "connect failed: " + err.Error()
	}
	logger.LogError("[%s] Failed to connect: %v", id, err)

	reason := msg
	if lifecycle.Default.OutdatedRecently(id) {
		// The 405 explains the failure better than the socket error.
		reason = lifecycle.Default.Get(id).LastError
	} else {
		lifecycle.Default.Set(id, lifecycle.PhaseFailed, msg)
	}
	cd.Instance.Connected = false
	cd.Instance.DisconnectReason = reason
	if uerr := w.instanceRepository.UpdateConnected(id, false, reason); uerr != nil {
		logger.LogError("[%s] Error updating instance: %s", id, uerr)
	}
	w.discardClient(id, client, "connect failed")
}

// applyHandshakeVersion makes the version fetched from web.whatsapp.com (or
// set by WHATSAPP_VERSION_*) reach the handshake, not only DeviceProps.
//
// The original code only filled store.DeviceProps.Version, which goes in the
// pairing payload. The version WhatsApp validates on every connection is
// store.GetWAVersion(), the library default — that is why the env vars never
// helped with the 405. Only a NEWER plausible version is applied: going below
// the library default would recreate the problem.
func applyHandshakeVersion(v clientVersion, logger *logger_wrapper.Logger, instanceId string) {
	if v.Major <= 0 || v.Minor <= 0 || v.Patch <= 0 {
		return
	}
	candidate := lifecycle.Version{uint32(v.Major), uint32(v.Minor), uint32(v.Patch)}
	current := store.GetWAVersion()
	if !lifecycle.Plausible(candidate) || !lifecycle.IsNewer(candidate, lifecycle.Version(current)) {
		return
	}
	store.SetWAVersion(store.WAVersionContainer(candidate))
	logger.LogInfo("[%s] Handshake version set to %s (library default was %s)", instanceId,
		store.GetWAVersion().String(), current.String())
}

// connectWithFallback is the connect sequence that StartClient had written
// twice (logged-in and new-device branches): one retry on EOF, and without
// proxy when the proxy refuses the credentials.
func (w whatsmeowService) connectWithFallback(cd *ClientData, client *whatsmeow.Client) error {
	id := cd.Instance.Id
	logger := w.loggerWrapper.GetLogger(id)
	err := client.Connect()
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "EOF") {
		logger.LogError("[%s] Erro de conexão WebSocket (EOF). Tentando reconectar em 5 segundos...", id)
		time.Sleep(5 * time.Second)
		if err = client.Connect(); err != nil {
			logger.LogError("[%s] Falha na segunda tentativa de conexão: %v", id, err)
		}
		return err
	}
	if strings.Contains(err.Error(), "username/password authentication failed") {
		logger.LogWarn("[%s] Proxy authentication failed, attempting to connect without proxy", id)
		client.SetProxy(nil)
		if err = client.Connect(); err != nil {
			logger.LogError("[%s] Failed to connect even without proxy: %v", id, err)
			return err
		}
		logger.LogInfo("[%s] Successfully connected without proxy", id)
		return nil
	}
	return err
}
