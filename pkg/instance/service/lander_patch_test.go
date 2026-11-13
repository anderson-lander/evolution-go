package instance_service

// Lander patch (2026-11-12): tests for the dead-client / 405 behaviour of
// Connect and GetQr. No network: the whatsmeow client is real but never
// connected (in-memory SQLite device store); repository and whatsmeow service
// are fakes that only implement what these paths call.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	_ "modernc.org/sqlite"

	"github.com/evolution-foundation/evolution-go/pkg/config"
	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	instance_repository "github.com/evolution-foundation/evolution-go/pkg/instance/repository"
	logger_wrapper "github.com/evolution-foundation/evolution-go/pkg/logger"
	"github.com/evolution-foundation/evolution-go/pkg/passkey/ceremony"
	"github.com/evolution-foundation/evolution-go/pkg/whatsmeow/lifecycle"
	whatsmeow_service "github.com/evolution-foundation/evolution-go/pkg/whatsmeow/service"
)

type fakeRepo struct {
	instance_repository.InstanceRepository
	mu   sync.Mutex
	inst map[string]*instance_model.Instance
}

func (f *fakeRepo) GetInstanceByID(id string) (*instance_model.Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, ok := f.inst[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	c := *i
	return &c, nil
}
func (f *fakeRepo) Update(i *instance_model.Instance) error { return nil }
func (f *fakeRepo) setQr(id, qr string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inst[id].Qrcode = qr
}

type fakeSvc struct {
	whatsmeow_service.WhatsmeowService
	mu            sync.Mutex
	clients       map[string]*whatsmeow.Client
	discarded     []string
	started       []string
	startClient   chan string
	onStart       func(id string)
	settingsError error
}

func (f *fakeSvc) DiscardClient(id, reason string) {
	f.mu.Lock()
	f.discarded = append(f.discarded, id)
	f.mu.Unlock()
	delete(f.clients, id)
}
func (f *fakeSvc) StartInstance(id string) error {
	f.mu.Lock()
	f.started = append(f.started, id)
	f.mu.Unlock()
	if f.onStart != nil {
		f.onStart(id)
	}
	return nil
}
func (f *fakeSvc) StartClient(cd *whatsmeow_service.ClientData) {
	if f.startClient != nil {
		f.startClient <- cd.Instance.Id
	}
}
func (f *fakeSvc) UpdateInstanceSettings(id string) error { return f.settingsError }
func (f *fakeSvc) PasskeyCeremonyStore() *ceremony.Store  { return nil }

func clienteParado(t *testing.T) *whatsmeow.Client {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_pragma=foreign_keys(1)", strings.ReplaceAll(t.Name(), "/", "_"))
	c, err := sqlstore.New(context.Background(), "sqlite", dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return whatsmeow.NewClient(c.NewDevice(), nil)
}

func montar(t *testing.T, id string) (*instances, *fakeRepo, *fakeSvc, map[string]*whatsmeow.Client) {
	t.Helper()
	ponteiros := map[string]*whatsmeow.Client{}
	repo := &fakeRepo{inst: map[string]*instance_model.Instance{id: {Id: id, Name: id, Token: "tok-" + id}}}
	svc := &fakeSvc{clients: ponteiros}
	cfg := &config.Config{LogDirectory: t.TempDir()}
	inst := &instances{
		instanceRepository: repo,
		config:             cfg,
		killChannel:        map[string](chan bool){},
		clientPointer:      ponteiros,
		whatsmeowService:   svc,
		loggerWrapper:      logger_wrapper.NewLoggerManager(cfg),
	}
	qrWait = 1500 * time.Millisecond
	return inst, repo, svc, ponteiros
}

func idUnico(t *testing.T) string { return "inst-" + strings.ReplaceAll(t.Name(), "/", "-") }

func TestGetQrTrocaClienteMorto(t *testing.T) {
	id := idUnico(t)
	inst, repo, svc, ponteiros := montar(t, id)
	ponteiros[id] = clienteParado(t) // registrado, não conectado, sem start: morto
	svc.onStart = func(id string) { repo.setQr(id, "QRBASE64|codigo") }

	qr, err := inst.GetQr(&instance_model.Instance{Id: id})
	if err != nil {
		t.Fatalf("esperava QR, veio erro: %v", err)
	}
	if qr.Qrcode != "QRBASE64" || qr.Code != "codigo" {
		t.Fatalf("QR errado: %+v", qr)
	}
	if len(svc.discarded) != 1 || len(svc.started) != 1 {
		t.Fatalf("o cliente morto tinha de ser descartado e um novo iniciado (descartados=%v iniciados=%v)", svc.discarded, svc.started)
	}
}

func TestGetQrNaoSobeSegundoClienteDuranteStart(t *testing.T) {
	id := idUnico(t)
	inst, repo, svc, ponteiros := montar(t, id)
	ponteiros[id] = clienteParado(t)
	if !lifecycle.Default.TryBeginStart(id) {
		t.Fatal("preparação")
	}
	defer lifecycle.Default.EndStart(id)
	go func() { time.Sleep(300 * time.Millisecond); repo.setQr(id, "Q|c") }()

	if _, err := inst.GetQr(&instance_model.Instance{Id: id}); err != nil {
		t.Fatalf("esperava o QR do start em andamento: %v", err)
	}
	if len(svc.started) != 0 || len(svc.discarded) != 0 {
		t.Fatalf("com um start em andamento, nada de descartar nem iniciar outro (descartados=%v iniciados=%v)", svc.discarded, svc.started)
	}
}

func TestGetQrDizQueAVersaoFoiRecusada(t *testing.T) {
	id := idUnico(t)
	inst, _, svc, _ := montar(t, id)
	svc.onStart = func(id string) {
		lifecycle.Default.MarkOutdated(id, whatsmeow_service.OutdatedDisconnectReason("2.3000.1"))
	}
	_, err := inst.GetQr(&instance_model.Instance{Id: id})
	if err == nil || !strings.Contains(err.Error(), "405") {
		t.Fatalf("o 405 tem de aparecer no erro, veio: %v", err)
	}
}

func TestGetQrFalhaDestaTentativaEncerraAEspera(t *testing.T) {
	id := idUnico(t)
	inst, _, svc, _ := montar(t, id)
	lifecycle.Default.Set(id, lifecycle.PhaseFailed, "falha antiga")
	time.Sleep(5 * time.Millisecond)
	svc.onStart = func(id string) { lifecycle.Default.Set(id, lifecycle.PhaseFailed, "dial tcp: timeout") }
	inicio := time.Now()
	_, err := inst.GetQr(&instance_model.Instance{Id: id})
	if err == nil || !strings.Contains(err.Error(), "dial tcp: timeout") {
		t.Fatalf("o erro desta tentativa tem de voltar, veio: %v", err)
	}
	if time.Since(inicio) > time.Second {
		t.Fatal("uma falha desta tentativa não pode esperar o prazo inteiro")
	}
}

func TestGetQrSemQrDentroDoPrazo(t *testing.T) {
	id := idUnico(t)
	inst, _, _, _ := montar(t, id)
	_, err := inst.GetQr(&instance_model.Instance{Id: id})
	if err == nil || !strings.Contains(err.Error(), "no QR code available") {
		t.Fatalf("sem QR no prazo, a mensagem de sempre: %v", err)
	}
}

func TestConnectTrocaClienteMorto(t *testing.T) {
	id := idUnico(t)
	inst, repo, svc, ponteiros := montar(t, id)
	ponteiros[id] = clienteParado(t)
	svc.startClient = make(chan string, 1)
	// UpdateInstanceSettings devolve nil quando o MyClient existe: era isso que
	// fazia o connect responder "already running" sem fazer nada.
	svc.settingsError = nil

	inst2, _ := repo.GetInstanceByID(id)
	if _, _, _, err := inst.Connect(&ConnectStruct{Subscribe: []string{"CONNECTION"}}, inst2); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-svc.startClient:
		if got != id {
			t.Fatalf("iniciou a instância errada: %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("o connect tinha de iniciar um cliente novo no lugar do morto")
	}
	if len(svc.discarded) != 1 {
		t.Fatalf("o cliente morto tinha de ser descartado: %v", svc.discarded)
	}
}

func TestConnectNaoMexeEmClienteIniciando(t *testing.T) {
	id := idUnico(t)
	inst, repo, svc, ponteiros := montar(t, id)
	ponteiros[id] = clienteParado(t)
	svc.startClient = make(chan string, 1)
	lifecycle.Default.TryBeginStart(id)
	defer lifecycle.Default.EndStart(id)

	inst2, _ := repo.GetInstanceByID(id)
	if _, _, _, err := inst.Connect(&ConnectStruct{}, inst2); err != nil {
		t.Fatal(err)
	}
	select {
	case <-svc.startClient:
		t.Fatal("com um start em andamento, o connect não pode iniciar outro")
	case <-time.After(300 * time.Millisecond):
	}
	if len(svc.discarded) != 0 {
		t.Fatal("cliente iniciando não é cliente morto")
	}
}

func TestStatusMostraOEstado(t *testing.T) {
	id := idUnico(t)
	inst, _, _, _ := montar(t, id)
	lifecycle.Default.MarkOutdated(id, "405: client outdated (x)")
	st, err := inst.Status(&instance_model.Instance{Id: id})
	if err != nil {
		t.Fatal(err)
	}
	if st.Connected || st.LoggedIn || st.State != "outdated" || !strings.HasPrefix(st.LastError, "405") {
		t.Fatalf("status sem o estado novo: %+v", st)
	}
}
