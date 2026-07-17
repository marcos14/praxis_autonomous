package scheduler

import (
	"context"
	"fmt"
	"strconv"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/gitops"
)

// KillerOrfaos mata as arvores de processos de harness orfaos que sobreviveram a
// uma queda do servico. Implementado por *procs.Registro (MatarOrfaos). Seam para
// testar a recuperacao sem processos reais.
type KillerOrfaos interface {
	MatarOrfaos() (int, error)
}

// Recuperacao resume o que a recuperacao pos-restart fez (observabilidade/testes).
type Recuperacao struct {
	ProjetosPreparados int // repos que passaram por PrepararRepoNoBoot (prune)
	OrfaosMortos       int // arvores de processos orfaos mortas
	DemandasRefiladas  int // demandas executando→pausada→refila (pronta)
}

// RecuperarPosRestart faz a recuperacao de boot exigida pela Fase 2i, apos uma
// queda/reinicio do servico, ANTES de o scheduler comecar a despachar:
//
//  1. para cada projeto ativo, `git worktree prune` (+ core.longpaths no Windows)
//     via gitops.PrepararRepoNoBoot — descarta registros de worktrees orfaos;
//  2. mata as arvores de processos de harness orfaos (killer.MatarOrfaos), para
//     nenhum processo sobrevivente segurar arquivos do worktree (Risco 5);
//  3. reconcilia as demandas presas em `executando` (cuja goroutine morreu com o
//     processo): executando → pausada → refila (pronta). O scheduler entao as
//     retoma automaticamente, do ponto onde pararam (a fase em andamento ficou
//     `pausada`/`pendente`; Preparar reaproveita o worktree; fases concluidas sao
//     puladas por proximaFase).
//
// Todos os passos sao best-effort: uma falha num projeto/demanda vira log e a
// recuperacao segue (nao vale abortar o boot inteiro por um repo problematico).
// O erro de retorno e reservado a falha de infraestrutura no proprio banco
// (listar projetos/demandas).
func RecuperarPosRestart(ctx context.Context, store *db.DB, git *gitops.Ops, killer KillerOrfaos, log func(string)) (Recuperacao, error) {
	if log == nil {
		log = func(string) {}
	}
	var rec Recuperacao
	if store == nil {
		return rec, fmt.Errorf("recuperacao sem Store")
	}

	// 1) prune dos worktrees de cada projeto ativo.
	if git != nil {
		projetos, err := store.ListarProjetos(ctx)
		if err != nil {
			return rec, fmt.Errorf("listar projetos: %w", err)
		}
		for _, p := range projetos {
			if !p.Ativo || p.Pasta == "" {
				continue
			}
			if err := git.PrepararRepoNoBoot(p.Pasta); err != nil {
				log("recuperacao: prune do projeto " + p.Slug + " falhou: " + err.Error())
				continue
			}
			rec.ProjetosPreparados++
		}
	}

	// 2) mata as arvores de processos orfaos.
	if killer != nil {
		mortos, err := killer.MatarOrfaos()
		if err != nil {
			log("recuperacao: matar orfaos falhou: " + err.Error())
		}
		rec.OrfaosMortos = mortos
	}

	// 3) executando → pausada → refila (pronta).
	dems, err := store.ListarDemandas(ctx, db.FiltroDemandas{Status: db.StatusDemandaExecutando})
	if err != nil {
		return rec, fmt.Errorf("listar demandas executando: %w", err)
	}
	for _, dem := range dems {
		if err := refilarPosRestart(ctx, store, dem); err != nil {
			log("recuperacao: refilar demanda " + strconv.FormatInt(dem.ID, 10) + " falhou: " + err.Error())
			continue
		}
		rec.DemandasRefiladas++
	}

	log(fmt.Sprintf("recuperacao pos-restart: %d projeto(s) preparado(s), %d orfao(s) morto(s), %d demanda(s) refilada(s)",
		rec.ProjetosPreparados, rec.OrfaosMortos, rec.DemandasRefiladas))
	return rec, nil
}

// refilarPosRestart aplica a transicao `executando → pausada → refila` de uma
// demanda orfa: primeiro a marca `pausada` (estado seguro — a execucao antiga foi
// interrompida pela queda), depois a re-enfileira em `pronta` para o scheduler
// retomar. Registra um evento de recuperacao com o rastro completo.
func refilarPosRestart(ctx context.Context, store *db.DB, dem db.Demanda) error {
	dem.Status = db.StatusDemandaPausada
	pausada, err := store.AtualizarDemanda(ctx, dem)
	if err != nil {
		return fmt.Errorf("marcar pausada: %w", err)
	}
	pausada.Status = db.StatusDemandaPronta
	if _, err := store.AtualizarDemanda(ctx, pausada); err != nil {
		return fmt.Errorf("refilar (pronta): %w", err)
	}
	pid := dem.ProjectID
	did := dem.ID
	_, _ = store.RegistrarEvento(ctx, db.Evento{
		ProjectID: &pid,
		DemandID:  &did,
		Tipo:      "recuperada_pos_restart",
		Titulo:    "Praxis: execução recuperada após reinício",
		Detalhe:   "O serviço reiniciou durante a execução desta demanda. Transição executando → pausada → refila: a demanda foi re-enfileirada e o scheduler a retomará a partir da próxima fase pendente.",
	})
	return nil
}
