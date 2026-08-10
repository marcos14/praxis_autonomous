package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Decoração de donos e visibilidade (Fase A do PLANO_MULTIUSUARIO.md): os
// campos DonoNome/Visibilidade/GrupoID de Projeto e Motor são CALCULADOS na
// leitura a partir do owner_user_id e das linhas de ACL (project_access/
// engine_access) — nunca persistidos. Uma consulta agregada por lista (nada de
// N+1): as listagens chamam decorarProjetos/decorarMotores ao final.

// resumoACL condensa as linhas de ACL de um recurso para o cálculo da
// visibilidade resumida.
type resumoACL struct {
	temUsuario bool
	grupoID    *int64
}

// visibilidade traduz o resumo em publica|privada|grupo (grupo vence quando há
// linha de grupo — é a informação mais útil na UI).
func (r resumoACL) visibilidade() string {
	switch {
	case r.grupoID != nil:
		return VisibilidadeGrupo
	case r.temUsuario:
		return VisibilidadePrivada
	default:
		return VisibilidadePublica
	}
}

// resumosACL agrega a tabela de ACL (project_access ou engine_access) por
// recurso. tabela/coluna são constantes internas — nunca entrada do usuário.
func (d *DB) resumosACL(ctx context.Context, tabela, coluna string) (map[int64]resumoACL, error) {
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT `+coluna+`, user_id, group_id FROM `+tabela)
	if err != nil {
		return nil, fmt.Errorf("resumir ACL de %s: %w", tabela, err)
	}
	defer rows.Close()

	out := map[int64]resumoACL{}
	for rows.Next() {
		var (
			id     int64
			userID sql.NullInt64
			grupo  sql.NullInt64
		)
		if err := rows.Scan(&id, &userID, &grupo); err != nil {
			return nil, err
		}
		r := out[id]
		if userID.Valid {
			r.temUsuario = true
		}
		// Primeiro grupo vence (a visibilidade simples da UI usa UM grupo; ACLs
		// finas com vários grupos continuam válidas — o resumo aponta o primeiro).
		if grupo.Valid && r.grupoID == nil {
			g := grupo.Int64
			r.grupoID = &g
		}
		out[id] = r
	}
	return out, rows.Err()
}

// nomesUsuarios devolve id→nome para o conjunto de ids (vazio → mapa vazio).
func (d *DB) nomesUsuarios(ctx context.Context, ids map[int64]bool) (map[int64]string, error) {
	if len(ids) == 0 {
		return map[int64]string{}, nil
	}
	marcas := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for id := range ids {
		marcas = append(marcas, "?")
		args = append(args, id)
	}
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT id, nome FROM users WHERE id IN (`+strings.Join(marcas, ",")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("nomes de usuários: %w", err)
	}
	defer rows.Close()

	out := map[int64]string{}
	for rows.Next() {
		var (
			id   int64
			nome string
		)
		if err := rows.Scan(&id, &nome); err != nil {
			return nil, err
		}
		out[id] = nome
	}
	return out, rows.Err()
}

// decorarMotores preenche DonoNome/Visibilidade/GrupoID dos motores.
func (d *DB) decorarMotores(ctx context.Context, motores []Motor) error {
	if len(motores) == 0 {
		return nil
	}
	resumos, err := d.resumosACL(ctx, "engine_access", "engine_id")
	if err != nil {
		return err
	}
	donos := map[int64]bool{}
	for _, m := range motores {
		if m.OwnerUserID != nil {
			donos[*m.OwnerUserID] = true
		}
	}
	nomes, err := d.nomesUsuarios(ctx, donos)
	if err != nil {
		return err
	}
	for i := range motores {
		r := resumos[motores[i].ID]
		motores[i].Visibilidade = r.visibilidade()
		motores[i].GrupoID = r.grupoID
		if motores[i].OwnerUserID != nil {
			motores[i].DonoNome = nomes[*motores[i].OwnerUserID]
		}
	}
	return nil
}

// decorarProjetos preenche DonoNome/Visibilidade/GrupoID dos projetos.
func (d *DB) decorarProjetos(ctx context.Context, projetos []Projeto) error {
	if len(projetos) == 0 {
		return nil
	}
	resumos, err := d.resumosACL(ctx, "project_access", "project_id")
	if err != nil {
		return err
	}
	donos := map[int64]bool{}
	for _, p := range projetos {
		if p.OwnerUserID != nil {
			donos[*p.OwnerUserID] = true
		}
	}
	nomes, err := d.nomesUsuarios(ctx, donos)
	if err != nil {
		return err
	}
	for i := range projetos {
		r := resumos[projetos[i].ID]
		projetos[i].Visibilidade = r.visibilidade()
		projetos[i].GrupoID = r.grupoID
		if projetos[i].OwnerUserID != nil {
			projetos[i].DonoNome = nomes[*projetos[i].OwnerUserID]
		}
	}
	return nil
}
