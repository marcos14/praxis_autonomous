package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/webpush"
)

// MaxFalhasPush é quantos envios seguidos recusados (429/5xx/transporte) uma
// assinatura tolera antes de ser descartada; um envio aceito zera a conta.
const MaxFalhasPush = 5

// timeoutPush limita cada POST ao serviço de push.
const timeoutPush = 10 * time.Second

// FontePush é o que o envio Web Push precisa do store: as assinaturas do
// usuário, o registro de sucesso/falha por assinatura, o carimbo push_em da
// notificação, as chaves VAPID e o contato do operador. Satisfeito por *db.DB.
type FontePush interface {
	ListarAssinaturasDoUsuario(ctx context.Context, userID int64) ([]db.AssinaturaPush, error)
	RemoverAssinaturaPush(ctx context.Context, endpoint string, userID int64) error
	RegistrarFalhaAssinatura(ctx context.Context, id int64, maxFalhas int) (bool, error)
	MarcarUsoAssinatura(ctx context.Context, id int64) error
	MarcarPushEnviado(ctx context.Context, id int64) error
	ObterOuGerarVAPID(ctx context.Context, gerar func() (publica, privada string, err error)) (string, string, error)
	ContatoPush(ctx context.Context) (string, error)
}

// payloadPush é o JSON cifrado que chega ao service worker (sw.js, evento
// `push`): o suficiente para mostrar a notificação e abrir a rota certa. Tag
// agrupa no sistema operacional as notificações do mesmo item (a mais nova
// substitui a anterior).
type payloadPush struct {
	ID      int64  `json:"id"`
	Titulo  string `json:"titulo"`
	Detalhe string `json:"detalhe,omitempty"`
	Rota    string `json:"rota,omitempty"`
	Tag     string `json:"tag"`
}

// chavesVAPID lê (uma vez) e memoriza o par VAPID da instância.
func (d *Despachante) chavesVAPID(ctx context.Context) (webpush.Chaves, error) {
	d.vapidMu.Lock()
	defer d.vapidMu.Unlock()
	if d.vapid.Publica != "" {
		return d.vapid, nil
	}
	pub, priv, err := d.push.ObterOuGerarVAPID(ctx, func() (string, string, error) {
		ch, err := webpush.GerarChaves()
		return ch.Publica, ch.Privada, err
	})
	if err != nil {
		return webpush.Chaves{}, err
	}
	d.vapid = webpush.Chaves{Publica: pub, Privada: priv}
	return d.vapid, nil
}

// enviarPush dispara o Web Push da notificação para cada assinatura do usuário,
// em goroutine (o ciclo do despachante não espera a rede). Sem FontePush, com
// push desligado nas preferências ou sem assinaturas, não faz nada.
func (d *Despachante) enviarPush(ctx context.Context, n db.Notificacao, prefs Preferencias) {
	if d.push == nil || !prefs.Push {
		return
	}
	assinaturas, err := d.push.ListarAssinaturasDoUsuario(ctx, n.UserID)
	if err != nil {
		d.log("notify: assinaturas push do usuário " + fmt.Sprint(n.UserID) + ": " + err.Error())
		return
	}
	if len(assinaturas) == 0 {
		return
	}
	chaves, err := d.chavesVAPID(ctx)
	if err != nil {
		d.log("notify: chaves vapid: " + err.Error())
		return
	}
	contato, err := d.push.ContatoPush(ctx)
	if err != nil {
		d.log("notify: contato do push: " + err.Error())
		return
	}
	tag := n.Rota
	if tag == "" {
		tag = n.Tipo
	}
	corpo, err := json.Marshal(payloadPush{ID: n.ID, Titulo: n.Titulo, Detalhe: n.Detalhe, Rota: n.Rota, Tag: tag})
	if err != nil {
		return
	}
	d.wgPush.Add(1)
	go func() {
		defer d.wgPush.Done()
		ctx, cancelar := context.WithTimeout(ctx, timeoutPush*time.Duration(len(assinaturas)))
		defer cancelar()
		algumAceito := false
		for _, a := range assinaturas {
			if d.enviarParaAssinatura(ctx, chaves, contato, a, corpo, n.Tipo) {
				algumAceito = true
			}
		}
		if algumAceito {
			if err := d.push.MarcarPushEnviado(ctx, n.ID); err != nil {
				d.log("notify: carimbar push da notificação " + fmt.Sprint(n.ID) + ": " + err.Error())
			}
		}
	}()
}

// enviarParaAssinatura faz um envio e aplica o resultado à assinatura: aceito
// → uso carimbado e falhas zeradas; 404/410 → assinatura apagada (o dispositivo
// cancelou); 429/5xx ou erro de transporte → falha contada, descarte na
// MaxFalhasPush-ésima; outros 4xx só vão ao log (não são culpa da assinatura).
// Devolve true quando o serviço aceitou.
func (d *Despachante) enviarParaAssinatura(ctx context.Context, chaves webpush.Chaves, contato string, a db.AssinaturaPush, corpo []byte, tipo string) bool {
	ctx, cancelar := context.WithTimeout(ctx, timeoutPush)
	defer cancelar()
	status, err := webpush.Enviar(ctx, d.cliPush, chaves, contato,
		webpush.Assinante{Endpoint: a.Endpoint, P256dh: a.P256dh, Auth: a.Auth}, corpo,
		webpush.Opcoes{Topico: tipo})
	// O store é atualizado fora do prazo do envio (um timeout na rede não pode
	// impedir o registro da falha).
	ctxStore, cancelarStore := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelarStore()
	switch {
	case err == nil && status >= 200 && status < 300:
		if err := d.push.MarcarUsoAssinatura(ctxStore, a.ID); err != nil {
			d.log("notify: marcar uso da assinatura " + fmt.Sprint(a.ID) + ": " + err.Error())
		}
		return true
	case err == nil && webpush.AssinaturaMorta(status):
		if err := d.push.RemoverAssinaturaPush(ctxStore, a.Endpoint, 0); err != nil {
			d.log("notify: remover assinatura morta " + fmt.Sprint(a.ID) + ": " + err.Error())
		} else {
			d.log(fmt.Sprintf("notify: assinatura push %d removida (serviço respondeu %d)", a.ID, status))
		}
	case err != nil || status == http.StatusTooManyRequests || status >= 500:
		motivo := fmt.Sprintf("status %d", status)
		if err != nil {
			motivo = err.Error()
		}
		removida, errF := d.push.RegistrarFalhaAssinatura(ctxStore, a.ID, MaxFalhasPush)
		if errF != nil {
			d.log("notify: registrar falha da assinatura " + fmt.Sprint(a.ID) + ": " + errF.Error())
		} else if removida {
			d.log(fmt.Sprintf("notify: assinatura push %d descartada após %d falhas (%s)", a.ID, MaxFalhasPush, motivo))
		} else {
			d.log(fmt.Sprintf("notify: push para a assinatura %d falhou (%s)", a.ID, motivo))
		}
	default:
		d.log(fmt.Sprintf("notify: push para a assinatura %d recusado (status %d)", a.ID, status))
	}
	return false
}

// AguardarEnvios espera os envios push em voo terminarem (shutdown e testes).
func (d *Despachante) AguardarEnvios() {
	d.wgPush.Wait()
}

// pushEstado agrupa o que o envio push guarda no despachante.
type pushEstado struct {
	push    FontePush
	cliPush *http.Client
	vapidMu sync.Mutex
	vapid   webpush.Chaves
	wgPush  sync.WaitGroup
}
