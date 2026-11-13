package lifecycle

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type relogio struct{ t time.Time }

func (r *relogio) agora() time.Time       { return r.t }
func (r *relogio) passar(d time.Duration) { r.t = r.t.Add(d) }

func novo() (*Registry, *relogio) {
	rel := &relogio{t: time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)}
	r := New()
	r.Now = rel.agora
	return r, rel
}

func TestSoUmStartPorVez(t *testing.T) {
	r, _ := novo()
	if !r.TryBeginStart("a") {
		t.Fatal("o primeiro start tinha de ser aceito")
	}
	if r.TryBeginStart("a") {
		t.Fatal("o segundo start da mesma instância não pode começar enquanto o primeiro corre")
	}
	if !r.TryBeginStart("b") {
		t.Fatal("outra instância não pode ser bloqueada")
	}
	r.EndStart("a")
	if !r.TryBeginStart("a") {
		t.Fatal("depois de terminar, um novo start tem de ser aceito")
	}
}

func TestStartTravadoNaoBloqueiaParaSempre(t *testing.T) {
	r, rel := novo()
	r.TryBeginStart("a")
	rel.passar(r.StartTimeout + time.Second)
	if r.IsStarting("a") {
		t.Fatal("um start mais velho que o prazo não pode contar como em andamento")
	}
	if !r.TryBeginStart("a") {
		t.Fatal("um start travado não pode impedir um novo para sempre")
	}
}

func TestStartsConcorrentesSoUmGanha(t *testing.T) {
	r, _ := novo()
	var ganhou atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r.TryBeginStart("x") {
				ganhou.Add(1)
			}
		}()
	}
	wg.Wait()
	if ganhou.Load() != 1 {
		t.Fatalf("exatamente um start deveria ganhar, ganharam %d", ganhou.Load())
	}
}

func TestQuarentenaDo405(t *testing.T) {
	r, rel := novo()
	r.MarkOutdated("a", "405: client outdated")
	if !r.OutdatedRecently("a") {
		t.Fatal("logo depois do 405 a instância está em quarentena")
	}
	if got := r.Get("a"); got.Phase != PhaseOutdated || got.LastError == "" {
		t.Fatalf("estado errado depois do 405: %+v", got)
	}
	// Um Disconnected/falha depois do 405 não apaga a marca.
	r.Set("a", PhaseFailed, "")
	if !r.OutdatedRecently("a") || r.Get("a").LastError == "" {
		t.Fatal("uma fase posterior sem erro não pode apagar a marca nem o erro do 405")
	}
	rel.passar(r.OutdatedCooldown + time.Second)
	if r.OutdatedRecently("a") {
		t.Fatal("a quarentena tem prazo")
	}
}

func TestConectarLimpaO405(t *testing.T) {
	r, _ := novo()
	r.MarkOutdated("a", "405")
	r.Set("a", PhaseQR, "")
	if r.OutdatedRecently("a") {
		t.Fatal("se o WhatsApp mandou QR, a versão foi aceita")
	}
	if r.Get("a").LastError != "" {
		t.Fatal("QR disponível não deve carregar o erro antigo")
	}
}

func TestInstanciaDesconhecidaEhIdle(t *testing.T) {
	r, _ := novo()
	if r.Get("nunca-vista").Phase != PhaseIdle {
		t.Fatal("instância sem cliente é idle")
	}
}

func TestFechaSoOPoolDoDono(t *testing.T) {
	r, _ := novo()
	antigo, novoDono := new(int), new(int)
	var fechouAntigo, fechouNovo int
	r.SetCloser("a", antigo, func() error { fechouAntigo++; return nil })
	prev := r.SetCloser("a", novoDono, func() error { fechouNovo++; return nil })
	if prev == nil {
		t.Fatal("trocar de dono devolve o fechador anterior para quem chamou")
	}
	_ = prev()
	// A goroutine do cliente antigo terminando não pode fechar o pool do novo.
	if err := r.CloseFor("a", antigo); err != nil {
		t.Fatal(err)
	}
	if fechouNovo != 0 {
		t.Fatal("o pool do cliente novo foi fechado por engano")
	}
	if err := r.CloseFor("a", novoDono); err != nil {
		t.Fatal(err)
	}
	if fechouAntigo != 1 || fechouNovo != 1 {
		t.Fatalf("cada pool fecha uma vez: antigo=%d novo=%d", fechouAntigo, fechouNovo)
	}
	if r.OpenResources() != 0 {
		t.Fatal("nada deve ficar registrado")
	}
	// Fechar de novo não faz nada.
	if err := r.CloseFor("a", novoDono); err != nil || fechouNovo != 1 {
		t.Fatal("fechar duas vezes não pode chamar o fechador de novo")
	}
}

func TestDonoAtual(t *testing.T) {
	r, _ := novo()
	velho, novoCli := new(int), new(int)
	if r.Owns("a", velho) {
		t.Fatal("sem registro, ninguém é dono")
	}
	r.SetCloser("a", velho, nil)
	if !r.Owns("a", velho) {
		t.Fatal("o cliente registrado é o dono")
	}
	r.SetCloser("a", novoCli, nil)
	if r.Owns("a", velho) || !r.Owns("a", novoCli) {
		t.Fatal("depois da troca, só o novo é dono — o laço do velho tem de sair")
	}
	_ = r.CloseFor("a", novoCli)
	if r.Owns("a", novoCli) {
		t.Fatal("descartado deixa de ser dono")
	}
}

func TestErroDoFechadorVolta(t *testing.T) {
	r, _ := novo()
	dono := new(int)
	r.SetCloser("a", dono, func() error { return errors.New("x") })
	if r.CloseFor("a", dono) == nil {
		t.Fatal("o erro ao fechar tem de voltar para ser registrado")
	}
}

func TestVersoes(t *testing.T) {
	antiga := Version{2, 3000, 1042386815}
	nova := Version{2, 3000, 1052556849}
	if !IsNewer(nova, antiga) || IsNewer(antiga, nova) || IsNewer(nova, nova) {
		t.Fatal("comparação de versão errada")
	}
	if !Plausible(nova) {
		t.Fatal("versão real tem de ser aceita")
	}
	for _, v := range []Version{{0, 0, 0}, {3, 3000, 1}, {2, 2412, 54}, {2, 3000, 0}} {
		if Plausible(v) {
			t.Fatalf("%v não é uma versão plausível do WhatsApp Web", v)
		}
	}
}
