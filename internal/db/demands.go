package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Origens possíveis de uma demanda (coluna demands.origem — CHECK no banco).
const (
	OrigemUI  = "ui"
	OrigemAPI = "api"
)

// Estados da máquina de demandas (coluna demands.status). São validados na
// aplicação, não por CHECK no banco, para não exigir migração a cada estado
// novo. Refletem o fluxo descrito no plano.
const (
	StatusDemandaRecebida            = "recebida"
	StatusDemandaAnalisando          = "analisando"
	StatusDemandaAguardandoRespostas = "aguardando_respostas"
	StatusDemandaPlanejando          = "planejando"
	StatusDemandaAguardandoAprovacao = "aguardando_aprovacao"
	StatusDemandaPronta              = "pronta"
	StatusDemandaExecutando          = "executando"
	StatusDemandaConcluida           = "concluida"
	StatusDemandaIntegrada           = "integrada"
	StatusDemandaPausada             = "pausada"
	StatusDemandaAguardandoFranquia  = "aguardando_franquia"
	StatusDemandaFalhou              = "falhou"
	StatusDemandaConflito            = "conflito"
	StatusDemandaCancelada           = "cancelada"
)

// Demanda é uma linha da tabela demands. As tags JSON refletem o modelo de
// dados do plano (snake_case) e são a forma serializada pela API.
type Demanda struct {
	ID           int64   `json:"id"`
	ProjectID    int64   `json:"project_id"`
	Titulo       string  `json:"titulo"`
	Origem       string  `json:"origem"`
	OrigemRef    string  `json:"origem_ref"`
	Status       string  `json:"status"`
	Prioridade   int     `json:"prioridade"`
	Branch       string  `json:"branch"`
	WorktreePath string  `json:"worktree_path"`
	PlanoMD      string  `json:"plano_md"`
	CustoUSD     float64 `json:"custo_usd"`
	BudgetUSD    float64 `json:"budget_usd"`
	Erro         string  `json:"erro"`
	// CriadoPor é o usuário logado que criou a demanda (users.id) — o AUTOR dos
	// commits das fases. Nulo em demandas de token de API/bootstrap (o autor cai
	// na identidade do próprio Praxis).
	CriadoPor    *int64 `json:"criado_por"`
	CriadoEm     string `json:"criado_em"`
	AtualizadoEm string `json:"atualizado_em"`
	// Visibilidade: privada (só o criador), grupo ou publica — ver visao.go.
	Visibilidade string `json:"visibilidade"`
	// CriadoPorNome é o nome do criador, resolvido por join em ListarDemandas e
	// ListarDemandasResumo (vazio fora delas e para demandas sem dono).
	CriadoPorNome string `json:"criado_por_nome,omitempty"`
}

// FiltroDemandas restringe ListarDemandas. Campos nulos/vazios não filtram.
type FiltroDemandas struct {
	ProjectID *int64 // filtra por projeto quando não-nil
	Status    string // filtra por status quando não-vazio
	// Visao aplica a ACL de projeto, a regra de dono e o escopo (valor zero =
	// sem restrição — chamadores internos como scheduler e recuperação).
	Visao Visao
}

// colunasDemanda lista as colunas de demands na ordem esperada por scanDemanda.
const colunasDemanda = `id, project_id, titulo, origem, origem_ref, status, prioridade,
	branch, worktree_path, plano_md, custo_usd, budget_usd, erro, criado_por, criado_em, atualizado_em,
	visibilidade`

// scanDemanda lê uma linha de demands (na ordem de colunasDemanda) para Demanda.
func scanDemanda(sc interface{ Scan(...any) error }) (Demanda, error) {
	var d Demanda
	var criadoPor sql.NullInt64
	if err := sc.Scan(&d.ID, &d.ProjectID, &d.Titulo, &d.Origem, &d.OrigemRef,
		&d.Status, &d.Prioridade, &d.Branch, &d.WorktreePath, &d.PlanoMD,
		&d.CustoUSD, &d.BudgetUSD, &d.Erro, &criadoPor, &d.CriadoEm, &d.AtualizadoEm,
		&d.Visibilidade); err != nil {
		return Demanda{}, err
	}
	d.CriadoPor = ptrDeNull(criadoPor)
	return d, nil
}

// scanDemandaComAutor é scanDemanda com a coluna extra do nome do criador
// (COALESCE(u.nome,'')) — usado pelas listagens que fazem join em users.
func scanDemandaComAutor(sc interface{ Scan(...any) error }) (Demanda, error) {
	var d Demanda
	var criadoPor sql.NullInt64
	if err := sc.Scan(&d.ID, &d.ProjectID, &d.Titulo, &d.Origem, &d.OrigemRef,
		&d.Status, &d.Prioridade, &d.Branch, &d.WorktreePath, &d.PlanoMD,
		&d.CustoUSD, &d.BudgetUSD, &d.Erro, &criadoPor, &d.CriadoEm, &d.AtualizadoEm,
		&d.Visibilidade, &d.CriadoPorNome); err != nil {
		return Demanda{}, err
	}
	d.CriadoPor = ptrDeNull(criadoPor)
	return d, nil
}

// normalizarVisibilidade aplica o default (privada) e valida a visibilidade de
// uma demanda a inserir.
func normalizarVisibilidade(dem *Demanda) error {
	if strings.TrimSpace(dem.Visibilidade) == "" {
		dem.Visibilidade = VisibilidadePrivada
	}
	if !VisibilidadeValida(dem.Visibilidade) {
		return ErrValorInvalido
	}
	return nil
}

// DefinirVisibilidadeDemanda muda quem enxerga a demanda (dono ou admin —
// checado na API). Valor desconhecido vira ErrValorInvalido; demanda
// inexistente, ErrNaoEncontrado.
func (d *DB) DefinirVisibilidadeDemanda(ctx context.Context, id int64, visibilidade string) error {
	if !VisibilidadeValida(visibilidade) {
		return ErrValorInvalido
	}
	res, err := d.Escritor.ExecContext(ctx,
		`UPDATE demands SET visibilidade = ? WHERE id = ?`, visibilidade, id)
	if err != nil {
		return fmt.Errorf("definir visibilidade da demanda %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("definir visibilidade da demanda %d: %w", id, err)
	}
	if n == 0 {
		return ErrNaoEncontrado
	}
	return nil
}

// DemandaVisivel informa se a demanda id é visível pela Visao: ACL do projeto
// dela E regra de dono. Inexistente → true (o handler responde 404 sem revelar
// se existe). Visão sem restrições devolve true sem consultar o banco.
func (d *DB) DemandaVisivel(ctx context.Context, id int64, v Visao) (bool, error) {
	cond, args := []string{}, []any{}
	cond, args = anexarCondAcesso(cond, args, "dm.project_id", v.ACL)
	cond, args = anexarCondDono(cond, args, "dm", v)
	if len(cond) == 0 {
		return true, nil
	}
	args = append(args, id)
	var ve bool
	err := d.Leitor.QueryRowContext(ctx,
		`SELECT (`+strings.Join(cond, " AND ")+`) FROM demands dm WHERE dm.id = ?`, args...).Scan(&ve)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("visibilidade da demanda %d: %w", id, err)
	}
	return ve, nil
}

// CriarDemanda insere uma nova demanda e devolve a linha persistida (com id,
// criado_em e atualizado_em preenchidos pelo banco). Origem/status vazios caem
// nos defaults (ui/recebida). Projeto inexistente vira ErrNaoEncontrado.
func (d *DB) CriarDemanda(ctx context.Context, dem Demanda) (Demanda, error) {
	if strings.TrimSpace(dem.Origem) == "" {
		dem.Origem = OrigemUI
	}
	if strings.TrimSpace(dem.Status) == "" {
		dem.Status = StatusDemandaRecebida
	}
	if err := normalizarVisibilidade(&dem); err != nil {
		return Demanda{}, err
	}
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO demands
			(project_id, titulo, origem, origem_ref, status, prioridade,
			 branch, worktree_path, plano_md, custo_usd, budget_usd, erro, criado_por, visibilidade)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		RETURNING id, criado_em, atualizado_em`,
		dem.ProjectID, dem.Titulo, dem.Origem, dem.OrigemRef, dem.Status, dem.Prioridade,
		dem.Branch, dem.WorktreePath, dem.PlanoMD, dem.CustoUSD, dem.BudgetUSD, dem.Erro,
		nullInt(dem.CriadoPor), dem.Visibilidade,
	)
	if err := row.Scan(&dem.ID, &dem.CriadoEm, &dem.AtualizadoEm); err != nil {
		return Demanda{}, traduzirErroFK(err)
	}
	return dem, nil
}

// CriarDemandaComFases insere uma demanda e suas fases numa única transação
// (tudo ou nada) e devolve a demanda persistida junto das fases criadas, na
// ordem informada. É o caminho canônico de criação de uma demanda "manual" (com
// fases já definidas, sem intake) usado pelo POST de demandas na Fase 2g.
//
// Origem/status vazios caem nos defaults (ui/recebida). Cada fase sem status cai
// em pendente. Erros conhecidos: projeto inexistente → ErrNaoEncontrado; código
// de fase repetido na demanda → ErrCodigoFaseDuplicado.
func (d *DB) CriarDemandaComFases(ctx context.Context, dem Demanda, fases []Fase) (Demanda, []Fase, error) {
	if strings.TrimSpace(dem.Origem) == "" {
		dem.Origem = OrigemUI
	}
	if strings.TrimSpace(dem.Status) == "" {
		dem.Status = StatusDemandaRecebida
	}
	if err := normalizarVisibilidade(&dem); err != nil {
		return Demanda{}, nil, err
	}

	tx, err := d.Escritor.BeginTx(ctx, nil)
	if err != nil {
		return Demanda{}, nil, fmt.Errorf("criar demanda com fases: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		INSERT INTO demands
			(project_id, titulo, origem, origem_ref, status, prioridade,
			 branch, worktree_path, plano_md, custo_usd, budget_usd, erro, criado_por, visibilidade)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		RETURNING id, criado_em, atualizado_em`,
		dem.ProjectID, dem.Titulo, dem.Origem, dem.OrigemRef, dem.Status, dem.Prioridade,
		dem.Branch, dem.WorktreePath, dem.PlanoMD, dem.CustoUSD, dem.BudgetUSD, dem.Erro,
		nullInt(dem.CriadoPor), dem.Visibilidade,
	)
	if err := row.Scan(&dem.ID, &dem.CriadoEm, &dem.AtualizadoEm); err != nil {
		return Demanda{}, nil, traduzirErroFK(err)
	}

	criadas := make([]Fase, 0, len(fases))
	for _, f := range fases {
		f.DemandID = dem.ID
		if strings.TrimSpace(f.Status) == "" {
			f.Status = StatusFasePendente
		}
		f.DependeDe = normalizarLista(f.DependeDe)
		deps, err := json.Marshal(f.DependeDe)
		if err != nil {
			return Demanda{}, nil, fmt.Errorf("codificar depende_de da fase %q: %w", f.Codigo, err)
		}
		frow := tx.QueryRowContext(ctx, `
			INSERT INTO phases
				(demand_id, codigo, titulo, status, depende_de, requer_humano,
				 gate_extra, modelo, tentativas, custo_usd, concluido_em, observacao, ordem)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
			RETURNING id`,
			f.DemandID, f.Codigo, f.Titulo, f.Status, string(deps), booleanParaInt(f.RequerHumano),
			f.GateExtra, f.Modelo, f.Tentativas, f.CustoUSD, f.ConcluidoEm, f.Observacao, f.Ordem,
		)
		if err := frow.Scan(&f.ID); err != nil {
			return Demanda{}, nil, traduzirErroFase(err)
		}
		criadas = append(criadas, f)
	}

	if err := tx.Commit(); err != nil {
		return Demanda{}, nil, fmt.Errorf("criar demanda com fases: %w", err)
	}
	return dem, criadas, nil
}

// ListarDemandas devolve as demandas que casam com o filtro, ordenadas por
// prioridade e, em empate, por id decrescente (mais recentes antes). Slice
// não-nil.
func (d *DB) ListarDemandas(ctx context.Context, f FiltroDemandas) ([]Demanda, error) {
	sqlStr := `SELECT ` + colunasDemandaPrefix("d") + `, COALESCE(u.nome, '')
		FROM demands d LEFT JOIN users u ON u.id = d.criado_por`
	cond := []string{}
	args := []any{}
	if f.ProjectID != nil {
		cond = append(cond, "d.project_id = ?")
		args = append(args, *f.ProjectID)
	}
	if strings.TrimSpace(f.Status) != "" {
		cond = append(cond, "d.status = ?")
		args = append(args, f.Status)
	}
	// Colunas qualificadas (d.*): dentro dos EXISTS das condições, um nome sem
	// qualificação resolveria para a tabela interna.
	cond, args = anexarCondAcesso(cond, args, "d.project_id", f.Visao.ACL)
	cond, args = anexarCondDono(cond, args, "d", f.Visao)
	cond, args = anexarCondEscopo(cond, args, "d", f.Visao)
	if len(cond) > 0 {
		sqlStr += " WHERE " + strings.Join(cond, " AND ")
	}
	sqlStr += " ORDER BY d.prioridade, d.id DESC"

	rows, err := d.Leitor.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("listar demandas: %w", err)
	}
	defer rows.Close()

	demandas := []Demanda{}
	for rows.Next() {
		dem, err := scanDemandaComAutor(rows)
		if err != nil {
			return nil, err
		}
		demandas = append(demandas, dem)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar demandas: %w", err)
	}
	return demandas, nil
}

// DemandaResumo enriquece a demanda com os agregados usados pelo quadro kanban
// (Fase 4a): total de fases, fases concluídas (para a barra de progresso) e o
// motor da execução mais recente (para o filtro por motor e o rótulo do card).
type DemandaResumo struct {
	Demanda
	FasesTotal      int    `json:"fases_total"`
	FasesConcluidas int    `json:"fases_concluidas"`
	Motor           string `json:"motor"`
}

// ListarDemandasResumo devolve as demandas do filtro já enriquecidas com os
// agregados do card do kanban (contagem de fases e motor da última execução),
// na mesma ordem de ListarDemandas (prioridade, id decrescente). Slice não-nil.
func (d *DB) ListarDemandasResumo(ctx context.Context, f FiltroDemandas) ([]DemandaResumo, error) {
	sqlStr := `SELECT ` + colunasDemandaPrefix("d") + `, COALESCE(u.nome, ''),
		(SELECT COUNT(*) FROM phases p WHERE p.demand_id = d.id) AS fases_total,
		(SELECT COUNT(*) FROM phases p WHERE p.demand_id = d.id AND p.status = '` + StatusFaseConcluida + `') AS fases_concluidas,
		COALESCE((SELECT r.engine FROM runs r WHERE r.demand_id = d.id AND r.engine <> '' ORDER BY r.id DESC LIMIT 1), '') AS motor
		FROM demands d LEFT JOIN users u ON u.id = d.criado_por`
	cond := []string{}
	args := []any{}
	if f.ProjectID != nil {
		cond = append(cond, "d.project_id = ?")
		args = append(args, *f.ProjectID)
	}
	if strings.TrimSpace(f.Status) != "" {
		cond = append(cond, "d.status = ?")
		args = append(args, f.Status)
	}
	cond, args = anexarCondAcesso(cond, args, "d.project_id", f.Visao.ACL)
	cond, args = anexarCondDono(cond, args, "d", f.Visao)
	cond, args = anexarCondEscopo(cond, args, "d", f.Visao)
	if len(cond) > 0 {
		sqlStr += " WHERE " + strings.Join(cond, " AND ")
	}
	sqlStr += " ORDER BY d.prioridade, d.id DESC"

	rows, err := d.Leitor.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("listar resumo de demandas: %w", err)
	}
	defer rows.Close()

	resumos := []DemandaResumo{}
	for rows.Next() {
		var r DemandaResumo
		var criadoPor sql.NullInt64
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Titulo, &r.Origem, &r.OrigemRef,
			&r.Status, &r.Prioridade, &r.Branch, &r.WorktreePath, &r.PlanoMD,
			&r.CustoUSD, &r.BudgetUSD, &r.Erro, &criadoPor, &r.CriadoEm, &r.AtualizadoEm,
			&r.Visibilidade, &r.CriadoPorNome,
			&r.FasesTotal, &r.FasesConcluidas, &r.Motor); err != nil {
			return nil, err
		}
		r.CriadoPor = ptrDeNull(criadoPor)
		resumos = append(resumos, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar resumo de demandas: %w", err)
	}
	return resumos, nil
}

// colunasDemandaPrefix devolve colunasDemanda com cada coluna qualificada por um
// alias de tabela (ex.: "d.id, d.project_id, ..."), para uso em consultas com
// JOIN/subconsulta onde as colunas precisam ser desambiguadas.
func colunasDemandaPrefix(alias string) string {
	partes := strings.Split(colunasDemanda, ",")
	for i, p := range partes {
		partes[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(partes, ", ")
}

// ReordenarDemandas reatribui a prioridade das demandas informadas conforme a
// posição na lista (0..N-1) — usado pelo arraste no kanban (Fase 4a), que só
// reordena a prioridade, nunca muda o status. Aceita um subconjunto das
// demandas; um id inexistente aborta a operação com ErrNaoEncontrado. A
// ordenação de ListarDemandas é por prioridade crescente, então posição menor =
// mais no topo.
func (d *DB) ReordenarDemandas(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := d.Escritor.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("reordenar demandas: %w", err)
	}
	defer tx.Rollback()
	for pos, id := range ids {
		res, err := tx.ExecContext(ctx,
			`UPDATE demands SET prioridade = ?, atualizado_em = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`,
			pos, id)
		if err != nil {
			return fmt.Errorf("reordenar demandas: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("reordenar demandas: %w", err)
		}
		if n == 0 {
			return ErrNaoEncontrado
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("reordenar demandas: %w", err)
	}
	return nil
}

// ObterDemanda devolve a demanda de id. Se não existir, devolve ErrNaoEncontrado.
func (d *DB) ObterDemanda(ctx context.Context, id int64) (Demanda, error) {
	row := d.Leitor.QueryRowContext(ctx,
		`SELECT `+colunasDemanda+` FROM demands WHERE id = ?`, id)
	dem, err := scanDemanda(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Demanda{}, ErrNaoEncontrado
	}
	if err != nil {
		return Demanda{}, fmt.Errorf("obter demanda %d: %w", id, err)
	}
	return dem, nil
}

// AtualizarDemanda grava os campos editáveis da demanda identificada por dem.ID
// e carimba atualizado_em com o horário atual. Devolve a linha resultante.
// Demanda inexistente vira ErrNaoEncontrado. O project_id não é alterado (a
// demanda não migra de projeto).
func (d *DB) AtualizarDemanda(ctx context.Context, dem Demanda) (Demanda, error) {
	res, err := d.Escritor.ExecContext(ctx, `
		UPDATE demands SET
			titulo = ?, origem = ?, origem_ref = ?, status = ?, prioridade = ?,
			branch = ?, worktree_path = ?, plano_md = ?, custo_usd = ?, budget_usd = ?,
			erro = ?, atualizado_em = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE id = ?`,
		dem.Titulo, dem.Origem, dem.OrigemRef, dem.Status, dem.Prioridade,
		dem.Branch, dem.WorktreePath, dem.PlanoMD, dem.CustoUSD, dem.BudgetUSD,
		dem.Erro, dem.ID,
	)
	if err != nil {
		return Demanda{}, fmt.Errorf("atualizar demanda %d: %w", dem.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Demanda{}, fmt.Errorf("atualizar demanda %d: %w", dem.ID, err)
	}
	if n == 0 {
		return Demanda{}, ErrNaoEncontrado
	}
	return d.ObterDemanda(ctx, dem.ID)
}

// RemoverDemanda apaga a demanda de id (e, por cascata, suas fases, execuções e
// eventos). Demanda inexistente vira ErrNaoEncontrado.
func (d *DB) RemoverDemanda(ctx context.Context, id int64) error {
	res, err := d.Escritor.ExecContext(ctx, `DELETE FROM demands WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("remover demanda %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("remover demanda %d: %w", id, err)
	}
	if n == 0 {
		return ErrNaoEncontrado
	}
	return nil
}

// traduzirErroFK converte violação de chave estrangeira (referência a um id
// inexistente, ex.: project_id/demand_id) em ErrNaoEncontrado.
func traduzirErroFK(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "FOREIGN KEY") {
		return ErrNaoEncontrado
	}
	return fmt.Errorf("persistir: %w", err)
}

// nullInt converte um *int64 para o valor aceito pelo driver (nil → NULL SQL).
func nullInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// ptrDeNull converte um sql.NullInt64 lido do banco para *int64 (NULL → nil).
func ptrDeNull(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}
