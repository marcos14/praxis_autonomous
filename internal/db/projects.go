package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrNaoEncontrado indica que a linha pedida não existe (ex.: projeto por id).
// Os handlers mapeiam para HTTP 404.
var ErrNaoEncontrado = errors.New("registro não encontrado")

// ErrSlugDuplicado indica violação da unicidade de projects.slug. Os handlers
// mapeiam para HTTP 409.
var ErrSlugDuplicado = errors.New("slug já em uso")

// Modos de integração de um projeto (coluna projects.modo_integracao — CHECK no
// banco). merge_request publica/empurra a branch da demanda a cada commit;
// merge_local integra por merge --no-ff local, sem push.
const (
	ModoIntegracaoMergeRequest = "merge_request"
	ModoIntegracaoMergeLocal   = "merge_local"
)

// Projeto é uma linha da tabela projects. As tags JSON refletem o modelo de
// dados do plano (snake_case) e são a forma serializada pela API.
type Projeto struct {
	ID              int64    `json:"id"`
	Nome            string   `json:"nome"`
	Slug            string   `json:"slug"`
	Pasta           string   `json:"pasta"`
	BranchPrincipal string   `json:"branch_principal"`
	ModoIntegracao  string   `json:"modo_integracao"`
	URLPlataforma   string   `json:"url_plataforma"`
	AddDirs         []string `json:"add_dirs"`
	Ativo           bool     `json:"ativo"`
	CriadoEm        string   `json:"criado_em"`
	// Overview de negócio do repositório (markdown, sem código) — contexto
	// injetado no consultor. Gravado por AtualizarOverview (não pelo CRUD comum).
	OverviewMD string `json:"overview_md"`
	OverviewEm string `json:"overview_em"`
	// OwnerUserID é o usuário que criou o projeto (NULL nos legados e nos
	// criados por token/bootstrap). O dono gerencia o próprio projeto mesmo sem
	// projetos.gerir (checado na camada da API).
	OwnerUserID *int64 `json:"owner_user_id,omitempty"`
	// SSHUserID aponta a chave SSH por usuário (PRAXIS_HOME/ssh/u<id>/) que
	// opera este repositório — clone, fetch/pull e push injetam o
	// GIT_SSH_COMMAND dela. NULL = credenciais do SO (comportamento histórico).
	SSHUserID *int64 `json:"ssh_user_id,omitempty"`
	// Campos CALCULADOS da ACL (project_access) — preenchidos na leitura, nunca
	// persistidos (ver decorarProjetos).
	DonoNome     string `json:"dono_nome,omitempty"`
	Visibilidade string `json:"visibilidade,omitempty"`
	GrupoID      *int64 `json:"grupo_id,omitempty"`
}

// colunasProjeto é a lista de colunas lidas nas consultas, na ordem esperada por
// scanProjeto.
const colunasProjeto = `id, nome, slug, pasta, branch_principal, modo_integracao,
	url_plataforma, add_dirs, ativo, criado_em, overview_md, overview_em, owner_user_id, ssh_user_id`

// scanProjeto lê uma linha de projects (na ordem de colunasProjeto) para Projeto,
// desserializando o add_dirs (JSON) e o ativo (0/1).
func scanProjeto(sc interface{ Scan(...any) error }) (Projeto, error) {
	var (
		p       Projeto
		addDirs string
		ativo   int
		owner   sql.NullInt64
		sshUser sql.NullInt64
	)
	if err := sc.Scan(&p.ID, &p.Nome, &p.Slug, &p.Pasta, &p.BranchPrincipal,
		&p.ModoIntegracao, &p.URLPlataforma, &addDirs, &ativo, &p.CriadoEm,
		&p.OverviewMD, &p.OverviewEm, &owner, &sshUser); err != nil {
		return Projeto{}, err
	}
	p.OwnerUserID = ptrDeNull(owner)
	p.SSHUserID = ptrDeNull(sshUser)
	p.Ativo = ativo != 0
	dirs, err := decodificarLista(addDirs)
	if err != nil {
		return Projeto{}, fmt.Errorf("decodificar add_dirs do projeto %d: %w", p.ID, err)
	}
	p.AddDirs = dirs
	return p, nil
}

// CriarProjeto insere um novo projeto e devolve a linha persistida (com id e
// criado_em preenchidos pelo banco). Slug duplicado vira ErrSlugDuplicado.
func (d *DB) CriarProjeto(ctx context.Context, p Projeto) (Projeto, error) {
	p.AddDirs = normalizarLista(p.AddDirs)
	addDirs, err := json.Marshal(p.AddDirs)
	if err != nil {
		return Projeto{}, fmt.Errorf("codificar add_dirs: %w", err)
	}
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO projects
			(nome, slug, pasta, branch_principal, modo_integracao, url_plataforma, add_dirs, ativo,
			 owner_user_id, ssh_user_id)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		RETURNING id, criado_em`,
		p.Nome, p.Slug, p.Pasta, p.BranchPrincipal, p.ModoIntegracao,
		p.URLPlataforma, string(addDirs), booleanParaInt(p.Ativo), nullInt(p.OwnerUserID),
		nullInt(p.SSHUserID),
	)
	if err := row.Scan(&p.ID, &p.CriadoEm); err != nil {
		return Projeto{}, traduzirErroProjeto(err)
	}
	return p, nil
}

// ListarProjetos devolve todos os projetos ordenados por nome (case-insensitive)
// e, em empate, por id.
func (d *DB) ListarProjetos(ctx context.Context) ([]Projeto, error) {
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT `+colunasProjeto+` FROM projects ORDER BY nome COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("listar projetos: %w", err)
	}
	defer rows.Close()

	projetos := []Projeto{}
	for rows.Next() {
		p, err := scanProjeto(rows)
		if err != nil {
			return nil, err
		}
		projetos = append(projetos, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar projetos: %w", err)
	}
	if err := d.decorarProjetos(ctx, projetos); err != nil {
		return nil, err
	}
	return projetos, nil
}

// ObterProjeto devolve o projeto de id. Se não existir, devolve ErrNaoEncontrado.
func (d *DB) ObterProjeto(ctx context.Context, id int64) (Projeto, error) {
	row := d.Leitor.QueryRowContext(ctx,
		`SELECT `+colunasProjeto+` FROM projects WHERE id = ?`, id)
	p, err := scanProjeto(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Projeto{}, ErrNaoEncontrado
	}
	if err != nil {
		return Projeto{}, fmt.Errorf("obter projeto %d: %w", id, err)
	}
	ps := []Projeto{p}
	if err := d.decorarProjetos(ctx, ps); err != nil {
		return Projeto{}, err
	}
	return ps[0], nil
}

// AtualizarProjeto grava os campos editáveis do projeto identificado por p.ID e
// devolve a linha resultante. Projeto inexistente vira ErrNaoEncontrado; slug em
// uso por outro projeto vira ErrSlugDuplicado.
func (d *DB) AtualizarProjeto(ctx context.Context, p Projeto) (Projeto, error) {
	p.AddDirs = normalizarLista(p.AddDirs)
	addDirs, err := json.Marshal(p.AddDirs)
	if err != nil {
		return Projeto{}, fmt.Errorf("codificar add_dirs: %w", err)
	}
	res, err := d.Escritor.ExecContext(ctx, `
		UPDATE projects SET
			nome = ?, slug = ?, pasta = ?, branch_principal = ?,
			modo_integracao = ?, url_plataforma = ?, add_dirs = ?, ativo = ?,
			ssh_user_id = ?
		WHERE id = ?`,
		p.Nome, p.Slug, p.Pasta, p.BranchPrincipal, p.ModoIntegracao,
		p.URLPlataforma, string(addDirs), booleanParaInt(p.Ativo),
		nullInt(p.SSHUserID), p.ID,
	)
	if err != nil {
		return Projeto{}, traduzirErroProjeto(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Projeto{}, fmt.Errorf("atualizar projeto %d: %w", p.ID, err)
	}
	if n == 0 {
		return Projeto{}, ErrNaoEncontrado
	}
	return d.ObterProjeto(ctx, p.ID)
}

// SSHUserDoRepo devolve o ssh_user_id do projeto DONO do caminho — a pasta
// principal do projeto ou o worktree de uma demanda dele. (nil, false) quando o
// caminho não pertence a projeto nenhum ou o projeto não tem credencial. É o
// resolvedor do gitops.AmbienteRede (Fase C): fetch/pull/push descobrem por
// aqui qual chave SSH usar.
func (d *DB) SSHUserDoRepo(ctx context.Context, caminho string) (*int64, bool) {
	caminho = strings.TrimSpace(caminho)
	if caminho == "" {
		return nil, false
	}
	var sshUser sql.NullInt64
	err := d.Leitor.QueryRowContext(ctx, `
		SELECT ssh_user_id FROM projects WHERE pasta = ?
		UNION ALL
		SELECT p.ssh_user_id FROM projects p
			JOIN demands dm ON dm.project_id = p.id
		WHERE dm.worktree_path = ?
		LIMIT 1`, caminho, caminho).Scan(&sshUser)
	if err != nil || !sshUser.Valid {
		return nil, false
	}
	v := sshUser.Int64
	return &v, true
}

// AtualizarOverview grava o overview do projeto (markdown já sanitizado pelo
// chamador) e carimba overview_em. Função dedicada — fora do AtualizarProjeto —
// para a geração em background não competir com edições do cadastro. Projeto
// inexistente vira ErrNaoEncontrado.
func (d *DB) AtualizarOverview(ctx context.Context, projectID int64, md string) error {
	res, err := d.Escritor.ExecContext(ctx, `
		UPDATE projects SET
			overview_md = ?, overview_em = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE id = ?`, md, projectID)
	if err != nil {
		return fmt.Errorf("atualizar overview do projeto %d: %w", projectID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("atualizar overview do projeto %d: %w", projectID, err)
	}
	if n == 0 {
		return ErrNaoEncontrado
	}
	return nil
}

// traduzirErroProjeto converte violações conhecidas em erros sentinela do pacote.
// A checagem é pela mensagem do modernc.org/sqlite ("UNIQUE constraint failed:
// projects.slug"), estável entre versões do driver.
func traduzirErroProjeto(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "projects.slug") {
		return ErrSlugDuplicado
	}
	// FK violada (owner_user_id/ssh_user_id apontando usuário inexistente).
	if strings.Contains(err.Error(), "FOREIGN KEY") {
		return ErrNaoEncontrado
	}
	return fmt.Errorf("persistir projeto: %w", err)
}

// booleanParaInt mapeia bool para o 0/1 esperado pelas colunas INTEGER CHECK.
func booleanParaInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// normalizarLista limpa uma lista de strings: apara espaços, descarta vazias e
// garante um slice não-nil (para serializar como [] e não null).
func normalizarLista(itens []string) []string {
	out := []string{}
	for _, it := range itens {
		if s := strings.TrimSpace(it); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// decodificarLista lê um TEXT com JSON de []string, tolerando vazio/null (que
// viram lista vazia).
func decodificarLista(bruto string) ([]string, error) {
	bruto = strings.TrimSpace(bruto)
	if bruto == "" || bruto == "null" {
		return []string{}, nil
	}
	var itens []string
	if err := json.Unmarshal([]byte(bruto), &itens); err != nil {
		return nil, err
	}
	if itens == nil {
		itens = []string{}
	}
	return itens, nil
}
