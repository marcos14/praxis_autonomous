package notify

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/webpush"
)

// pushFake implementa FontePush em memória.
type pushFake struct {
	mu          sync.Mutex
	assinaturas map[int64][]db.AssinaturaPush // por usuário
	usos        map[int64]int                 // id da assinatura → envios aceitos
	pushEnviado map[int64]int                 // id da notificação → carimbos
	vapidGerado int
}

func novoPushFake() *pushFake {
	return &pushFake{assinaturas: map[int64][]db.AssinaturaPush{}, usos: map[int64]int{}, pushEnviado: map[int64]int{}}
}

func (f *pushFake) ListarAssinaturasDoUsuario(_ context.Context, userID int64) ([]db.AssinaturaPush, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]db.AssinaturaPush(nil), f.assinaturas[userID]...), nil
}

func (f *pushFake) RemoverAssinaturaPush(_ context.Context, endpoint string, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for uid, lista := range f.assinaturas {
		var resto []db.AssinaturaPush
		for _, a := range lista {
			if a.Endpoint != endpoint {
				resto = append(resto, a)
			}
		}
		f.assinaturas[uid] = resto
	}
	return nil
}

func (f *pushFake) RegistrarFalhaAssinatura(_ context.Context, id int64, maxFalhas int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for uid, lista := range f.assinaturas {
		for i := range lista {
			if lista[i].ID != id {
				continue
			}
			lista[i].Falhas++
			if lista[i].Falhas >= maxFalhas {
				f.assinaturas[uid] = append(lista[:i], lista[i+1:]...)
				return true, nil
			}
			return false, nil
		}
	}
	return false, db.ErrNaoEncontrado
}

func (f *pushFake) MarcarUsoAssinatura(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.usos[id]++
	for _, lista := range f.assinaturas {
		for i := range lista {
			if lista[i].ID == id {
				lista[i].Falhas = 0
			}
		}
	}
	return nil
}

func (f *pushFake) MarcarPushEnviado(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushEnviado[id]++
	return nil
}

func (f *pushFake) ObterOuGerarVAPID(_ context.Context, gerar func() (string, string, error)) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vapidGerado++
	return gerar()
}

func (f *pushFake) ContatoPush(context.Context) (string, error) { return "mailto:ops@x.com", nil }

func (f *pushFake) falhas(id int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, lista := range f.assinaturas {
		for _, a := range lista {
			if a.ID == id {
				return a.Falhas
			}
		}
	}
	return -1
}

func (f *pushFake) existe(id int64) bool { return f.falhas(id) >= 0 }

// chavesDeDispositivo gera um par p256dh/auth válido como o navegador faria.
func chavesDeDispositivo(t *testing.T) (p256dh, auth string) {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	segredo := make([]byte, 16)
	_, _ = rand.Read(segredo)
	return base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(segredo)
}

// servicoPushFalso responde conforme o sufixo do endpoint: /ok → 201,
// /morta → 410, /fora → 503, /grande → 413. Conta os POSTs por caminho.
type servicoPushFalso struct {
	mu       sync.Mutex
	chamadas map[string]int
	topicos  []string
}

func (s *servicoPushFalso) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.chamadas[r.URL.Path]++
		s.topicos = append(s.topicos, r.Header.Get("Topic"))
		s.mu.Unlock()
		if !strings.HasPrefix(r.Header.Get("Authorization"), "vapid t=") || r.Header.Get("Content-Encoding") != "aes128gcm" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/morta"):
			w.WriteHeader(http.StatusGone)
		case strings.HasSuffix(r.URL.Path, "/fora"):
			w.WriteHeader(http.StatusServiceUnavailable)
		case strings.HasSuffix(r.URL.Path, "/grande"):
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		default:
			w.WriteHeader(http.StatusCreated)
		}
	}
}

func (s *servicoPushFalso) n(caminho string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chamadas[caminho]
}

func TestDespachanteEnviaPushEAplicaResultados(t *testing.T) {
	svc := &servicoPushFalso{chamadas: map[string]int{}}
	ts := httptest.NewServer(svc.handler())
	defer ts.Close()

	p256dh, auth := chavesDeDispositivo(t)
	pf := novoPushFake()
	pf.assinaturas[42] = []db.AssinaturaPush{
		{ID: 1, UserID: 42, Endpoint: ts.URL + "/ok", P256dh: p256dh, Auth: auth},
		{ID: 2, UserID: 42, Endpoint: ts.URL + "/morta", P256dh: p256dh, Auth: auth},
		{ID: 3, UserID: 42, Endpoint: ts.URL + "/fora", P256dh: p256dh, Auth: auth, Falhas: MaxFalhasPush - 2},
		{ID: 4, UserID: 42, Endpoint: ts.URL + "/grande", P256dh: p256dh, Auth: auth},
	}
	pf.assinaturas[43] = []db.AssinaturaPush{{ID: 5, UserID: 43, Endpoint: ts.URL + "/ok", P256dh: p256dh, Auth: auth}}
	u := novoUsuariosFake()
	u.consultas[7] = ptr(42)
	u.consultas[8] = ptr(43)
	u.prefs[43] = `{"push":false}`
	fonte := &fonteFake{}
	fonte.adicionar(db.Evento{ID: 1, Tipo: "consulta_respondida", Titulo: "Praxis: consulta respondida", ConsultaID: ptr(7)})
	fonte.adicionar(db.Evento{ID: 2, Tipo: "consulta_respondida", Titulo: "push desligado", ConsultaID: ptr(8)})

	var logs []string
	var logMu sync.Mutex
	desp := NovoDespachante(OpcoesDespachante{
		Fonte:       fonte,
		Usuarios:    u,
		Push:        pf,
		ClientePush: ts.Client(),
		Config:      func(context.Context) (Config, error) { return Config{}, nil },
		Log:         func(m string) { logMu.Lock(); logs = append(logs, m); logMu.Unlock() },
	})
	desp.processar(context.Background(), 0)
	desp.AguardarEnvios()

	// Um POST por assinatura do usuário 42; o 43 desligou o push.
	for caminho, quer := range map[string]int{"/ok": 1, "/morta": 1, "/fora": 1, "/grande": 1} {
		if got := svc.n(caminho); got != quer {
			t.Errorf("POSTs em %s = %d, quer %d", caminho, got, quer)
		}
	}
	if pf.usos[1] != 1 || pf.pushEnviado[1] != 1 {
		t.Fatalf("aceito: usos=%v push_em=%v", pf.usos, pf.pushEnviado)
	}
	if pf.existe(2) {
		t.Fatal("410 deveria apagar a assinatura")
	}
	if pf.falhas(3) != MaxFalhasPush-1 {
		t.Fatalf("503 deveria contar falha: %d", pf.falhas(3))
	}
	if pf.falhas(4) != 0 {
		t.Fatalf("413 não é culpa da assinatura: falhas=%d", pf.falhas(4))
	}
	if pf.pushEnviado[2] != 0 {
		t.Fatal("notificação do usuário sem push não deveria ter push_em")
	}
	if pf.vapidGerado != 1 {
		t.Fatalf("chaves vapid lidas %d vezes (quer 1: memorizadas)", pf.vapidGerado)
	}
	svc.mu.Lock()
	topico := svc.topicos[0]
	svc.mu.Unlock()
	if topico != "consulta_respondida" {
		t.Fatalf("Topic = %q", topico)
	}

	// Segunda falha seguida no /fora atinge o limite → descartada.
	fonte.adicionar(db.Evento{ID: 3, Tipo: "consulta_falhou", Titulo: "de novo", ConsultaID: ptr(7)})
	desp.processar(context.Background(), 2)
	desp.AguardarEnvios()
	if pf.existe(3) {
		t.Fatal("assinatura deveria ser descartada ao atingir MaxFalhasPush")
	}
	if pf.usos[1] != 2 || pf.pushEnviado[3] != 1 {
		t.Fatalf("segundo envio: usos=%v push_em=%v", pf.usos, pf.pushEnviado)
	}
	logMu.Lock()
	defer logMu.Unlock()
	var descarte bool
	for _, l := range logs {
		if strings.Contains(l, "assinatura push 3 descartada") {
			descarte = true
		}
	}
	if !descarte {
		t.Fatalf("faltou o log de descarte: %q", logs)
	}
}

func TestDespachanteSemAssinaturasNaoChamaRede(t *testing.T) {
	pf := novoPushFake()
	u := novoUsuariosFake()
	u.consultas[7] = ptr(42)
	fonte := &fonteFake{}
	fonte.adicionar(db.Evento{ID: 1, Tipo: "consulta_respondida", ConsultaID: ptr(7)})
	desp := NovoDespachante(OpcoesDespachante{
		Fonte:    fonte,
		Usuarios: u,
		Push:     pf,
		Config:   func(context.Context) (Config, error) { return Config{}, nil },
	})
	desp.processar(context.Background(), 0)
	desp.AguardarEnvios()
	if pf.vapidGerado != 0 || len(pf.pushEnviado) != 0 {
		t.Fatalf("sem assinaturas nada deveria acontecer: vapid=%d push=%v", pf.vapidGerado, pf.pushEnviado)
	}
	if len(u.notificacoes()) != 1 {
		t.Fatal("a caixa de entrada continua sendo gravada")
	}
	_ = webpush.Chaves{}
}
