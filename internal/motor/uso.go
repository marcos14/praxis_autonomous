package motor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// JanelaFranquia é uma janela de limite do vendor (ex.: 5h e semanal no Codex),
// já normalizada: só o rótulo, o percentual usado e o horário de reset. Nenhum
// identificador de conta/plano do vendor é exposto.
type JanelaFranquia struct {
	Rotulo   string     `json:"rotulo"`
	UsadoPct float64    `json:"usado_pct"`
	ResetEm  *time.Time `json:"reset_em,omitempty"`
}

// UsoFranquia é a visão segura da franquia de um perfil junto ao vendor.
// Disponivel=false com Mensagem explica por que não há leitura (CLI sem API
// headless, versão sem o contrato, perfil deslogado, timeout).
type UsoFranquia struct {
	Vendor       string           `json:"vendor"`
	Disponivel   bool             `json:"disponivel"`
	Mensagem     string           `json:"mensagem,omitempty"`
	Janelas      []JanelaFranquia `json:"janelas,omitempty"`
	VerificadoEm time.Time        `json:"verificado_em"`
}

// ConsultarFranquia lê o uso da franquia do perfil junto ao CLI do vendor.
//   - Claude: o CLI só expõe /usage na tela interativa; não fazemos scraping —
//     devolve indisponível e a UI acompanha pelo consumo local do Praxis.
//   - Codex: usa o app-server (account/rateLimits/read). Versão sem o contrato
//     vira indisponível com mensagem, nunca parsing de credenciais.
func ConsultarFranquia(ctx context.Context, vendor, perfilDir string) UsoFranquia {
	return consultarFranquiaCom(ctx, vendor, perfilDir, nil)
}

func consultarFranquiaCom(ctx context.Context, vendor, perfilDir string, comando fabricaComando) UsoFranquia {
	if comando == nil {
		comando = exec.CommandContext
	}
	vendor = normalizarNomeMotor(vendor)
	u := UsoFranquia{Vendor: vendor, VerificadoEm: time.Now().UTC()}
	if !VendorComPerfilIsolado(vendor) {
		u.Mensagem = "motor sem leitura de franquia"
		return u
	}
	switch vendor {
	case "claude":
		u.Mensagem = "o Claude não expõe a franquia de forma headless; acompanhe pelo consumo do Praxis"
		return u
	case "codex":
		return franquiaCodex(ctx, perfilDir, comando, u)
	}
	return u
}

// franquiaCodex conversa com o app-server do Codex por stdio: initialize e
// account/rateLimits/read. Qualquer desvio do contrato vira indisponível.
func franquiaCodex(ctx context.Context, perfilDir string, comando fabricaComando, u UsoFranquia) UsoFranquia {
	dir, err := PrepararPerfil("codex", perfilDir)
	if err != nil {
		u.Mensagem = "diretório isolado do perfil indisponível"
		return u
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	cmd := comando(ctx, ResolverCLI("codex"), "app-server", "--listen", "stdio://")
	if err := aplicarPerfil(cmd, "codex", dir); err != nil {
		u.Mensagem = "não foi possível aplicar o perfil Codex"
		return u
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		u.Mensagem = "não foi possível preparar o app-server do Codex"
		return u
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		u.Mensagem = "não foi possível ler o app-server do Codex"
		return u
	}
	cmd.Stderr = io.Discard
	prepararProcessoFilho(cmd)
	if err := cmd.Start(); err != nil {
		if erroExecutavelAusente(err) {
			u.Mensagem = "CLI codex não encontrado no PATH do serviço"
		} else {
			u.Mensagem = "não foi possível iniciar o app-server do Codex"
		}
		return u
	}
	defer func() {
		_ = stdin.Close()
		if cmd.ProcessState == nil {
			_ = cmd.Cancel()
		}
		_ = cmd.Wait()
	}()

	enc := json.NewEncoder(stdin)
	if err := enc.Encode(map[string]any{
		"id": 1, "method": "initialize",
		"params": map[string]any{"clientInfo": map[string]any{"name": "praxis-autonomous", "title": "Praxis Autonomous", "version": "1"}},
	}); err != nil {
		u.Mensagem = "não foi possível inicializar o app-server do Codex"
		return u
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 128*1024), 4*1024*1024)
	for sc.Scan() {
		var msg mensagemRPC
		if json.Unmarshal(sc.Bytes(), &msg) != nil {
			continue
		}
		switch strings.Trim(string(msg.ID), `"`) {
		case "1":
			if len(msg.Error) > 0 && string(msg.Error) != "null" {
				u.Mensagem = "a versão instalada do Codex recusou a inicialização do app-server"
				return u
			}
			_ = enc.Encode(map[string]any{"method": "initialized"})
			if err := enc.Encode(map[string]any{"id": 2, "method": "account/rateLimits/read"}); err != nil {
				u.Mensagem = "não foi possível pedir a franquia ao Codex"
				return u
			}
		case "2":
			if len(msg.Error) > 0 && string(msg.Error) != "null" {
				u.Mensagem = "a versão instalada do Codex não expõe a franquia pelo app-server"
				return u
			}
			janelas, err := decodificarJanelasCodex(msg.Result, u.VerificadoEm)
			if err != nil {
				u.Mensagem = "o Codex devolveu uma resposta de franquia incompatível"
				return u
			}
			u.Disponivel = true
			u.Janelas = janelas
			return u
		}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		u.Mensagem = "a leitura da franquia do Codex excedeu o tempo limite"
	} else {
		u.Mensagem = "o app-server do Codex encerrou antes de responder a franquia"
	}
	return u
}

// janelaCodexBruta tolera as duas grafias vistas nos contratos do Codex
// (camelCase no app-server, snake_case nos eventos do exec).
type janelaCodexBruta struct {
	UsedPercent      *float64 `json:"usedPercent"`
	UsedPercentSnake *float64 `json:"used_percent"`
	WindowMinutes    *int64   `json:"windowMinutes"`
	WindowMinSnake   *int64   `json:"window_minutes"`
	ResetsInSeconds  *int64   `json:"resetsInSeconds"`
	ResetsSnake      *int64   `json:"resets_in_seconds"`
}

func (j janelaCodexBruta) normalizar(rotuloPadrao string, base time.Time) (JanelaFranquia, bool) {
	usado := j.UsedPercent
	if usado == nil {
		usado = j.UsedPercentSnake
	}
	if usado == nil {
		return JanelaFranquia{}, false
	}
	out := JanelaFranquia{Rotulo: rotuloPadrao, UsadoPct: *usado}
	minutos := j.WindowMinutes
	if minutos == nil {
		minutos = j.WindowMinSnake
	}
	if minutos != nil && *minutos > 0 {
		out.Rotulo = rotuloJanela(*minutos)
	}
	segundos := j.ResetsInSeconds
	if segundos == nil {
		segundos = j.ResetsSnake
	}
	if segundos != nil && *segundos > 0 {
		t := base.Add(time.Duration(*segundos) * time.Second)
		out.ResetEm = &t
	}
	return out, true
}

// decodificarJanelasCodex extrai as janelas de um resultado do app-server,
// aceitando o envelope {"rateLimits": {...}} ou as janelas na raiz.
func decodificarJanelasCodex(bruto json.RawMessage, base time.Time) ([]JanelaFranquia, error) {
	var corpo struct {
		RateLimits *struct {
			Primary   *janelaCodexBruta `json:"primary"`
			Secondary *janelaCodexBruta `json:"secondary"`
		} `json:"rateLimits"`
		Primary   *janelaCodexBruta `json:"primary"`
		Secondary *janelaCodexBruta `json:"secondary"`
	}
	if err := json.Unmarshal(bruto, &corpo); err != nil {
		return nil, err
	}
	primary, secondary := corpo.Primary, corpo.Secondary
	if corpo.RateLimits != nil {
		primary, secondary = corpo.RateLimits.Primary, corpo.RateLimits.Secondary
	}
	var janelas []JanelaFranquia
	if primary != nil {
		if j, ok := primary.normalizar("sessão", base); ok {
			janelas = append(janelas, j)
		}
	}
	if secondary != nil {
		if j, ok := secondary.normalizar("semana", base); ok {
			janelas = append(janelas, j)
		}
	}
	if len(janelas) == 0 {
		return nil, fmt.Errorf("resposta sem janelas de franquia")
	}
	return janelas, nil
}

// rotuloJanela converte a duração da janela num rótulo curto ("5h", "7d").
func rotuloJanela(minutos int64) string {
	switch {
	case minutos%(24*60) == 0:
		return fmt.Sprintf("%dd", minutos/(24*60))
	case minutos%60 == 0:
		return fmt.Sprintf("%dh", minutos/60)
	default:
		return fmt.Sprintf("%dmin", minutos)
	}
}
