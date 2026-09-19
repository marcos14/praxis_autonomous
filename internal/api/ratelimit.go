package api

import (
	"sync"
	"time"
)

// Limite de tentativas de login (M1 do PLANO_INTERNET): depois de
// limiteFalhasLogin falhas na janela janelaFalhasLogin, a chave (IP ou e-mail)
// fica bloqueada até a falha mais antiga sair da janela. Em memória: reiniciar
// o serviço zera — suficiente contra força bruta pela internet, sem tabela nova.
const (
	limiteFalhasLogin = 10
	janelaFalhasLogin = 15 * time.Minute
	// limiarPodaLimitador é a quantidade de chaves a partir da qual uma falha
	// nova também varre as chaves antigas (para o mapa não crescer sem fim com
	// IPs que erraram uma vez e nunca voltaram).
	limiarPodaLimitador = 5000
)

// limitadorLogin conta falhas por chave numa janela deslizante.
type limitadorLogin struct {
	mu     sync.Mutex
	limite int
	janela time.Duration
	agora  func() time.Time // injetável nos testes
	falhas map[string][]time.Time
}

// novoLimitadorLogin monta o limitador com limite e janela dados.
func novoLimitadorLogin(limite int, janela time.Duration) *limitadorLogin {
	return &limitadorLogin{limite: limite, janela: janela, agora: time.Now, falhas: map[string][]time.Time{}}
}

// bloqueado informa se a chave atingiu o limite na janela e, nesse caso, quanto
// falta para a falha mais antiga sair dela (mínimo 1s — é o Retry-After).
func (l *limitadorLogin) bloqueado(chave string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	agora := l.agora()
	recentes := l.podar(chave, agora)
	if len(recentes) < l.limite {
		return false, 0
	}
	espera := recentes[0].Add(l.janela).Sub(agora)
	if espera < time.Second {
		espera = time.Second
	}
	return true, espera
}

// registrarFalha conta uma falha para a chave (agora).
func (l *limitadorLogin) registrarFalha(chave string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	agora := l.agora()
	if len(l.falhas) >= limiarPodaLimitador {
		for k := range l.falhas {
			l.podar(k, agora)
		}
	}
	l.falhas[chave] = append(l.podar(chave, agora), agora)
}

// limpar zera as falhas da chave (login bem-sucedido).
func (l *limitadorLogin) limpar(chave string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.falhas, chave)
}

// podar descarta as falhas da chave fora da janela e devolve as que restam,
// já regravadas (uma chave sem falhas some do mapa). Chamar com o mutex tomado.
func (l *limitadorLogin) podar(chave string, agora time.Time) []time.Time {
	limiar := agora.Add(-l.janela)
	lista := l.falhas[chave]
	i := 0
	for i < len(lista) && !lista[i].After(limiar) {
		i++
	}
	lista = lista[i:]
	if len(lista) == 0 {
		delete(l.falhas, chave)
		return nil
	}
	l.falhas[chave] = lista
	return lista
}
