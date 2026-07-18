package db

import (
	"context"
	"errors"
	"testing"
)

func TestGrupoUsuariosCRUDEVinculo(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	motor, err := d.CriarMotor(ctx, Motor{
		Nome: "claude", Ativo: true, ModeloExec: "opus",
		ModeloAnalise: "sonnet", ModeloConsulta: "haiku",
	})
	if err != nil {
		t.Fatalf("CriarMotor: %v", err)
	}
	if m, err := d.ObterMotor(ctx, motor.ID); err != nil || m.ModeloConsulta != "haiku" {
		t.Fatalf("modelo_consulta não persistiu: %+v err=%v", m, err)
	}

	g, err := d.CriarGrupoUsuarios(ctx, GrupoUsuarios{
		Nome: "Suporte", Descricao: "time de suporte", EngineID: &motor.ID, Modelo: "haiku-rapido",
	})
	if err != nil {
		t.Fatalf("CriarGrupoUsuarios: %v", err)
	}
	if g.ID == 0 || g.CriadoEm == "" {
		t.Fatalf("grupo sem id/criado_em: %+v", g)
	}

	// nome duplicado.
	if _, err := d.CriarGrupoUsuarios(ctx, GrupoUsuarios{Nome: "Suporte"}); !errors.Is(err, ErrGrupoUsuariosDuplicado) {
		t.Fatalf("duplicado = %v, quero ErrGrupoUsuariosDuplicado", err)
	}
	// motor inexistente.
	ruim := int64(999)
	if _, err := d.CriarGrupoUsuarios(ctx, GrupoUsuarios{Nome: "X", EngineID: &ruim}); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("motor inexistente = %v, quero ErrNaoEncontrado", err)
	}

	u, err := d.CriarUsuario(ctx, "Ana", "ana@x.com", "senha-forte-123", nil)
	if err != nil {
		t.Fatalf("CriarUsuario: %v", err)
	}

	// sem grupo.
	if _, ok, err := d.GrupoDoUsuario(ctx, u.ID); err != nil || ok {
		t.Fatalf("GrupoDoUsuario antes = ok %v err %v, quero false", ok, err)
	}

	// vincular.
	if err := d.DefinirGrupoDoUsuario(ctx, u.ID, &g.ID); err != nil {
		t.Fatalf("DefinirGrupoDoUsuario: %v", err)
	}
	got, ok, err := d.GrupoDoUsuario(ctx, u.ID)
	if err != nil || !ok || got.ID != g.ID || got.Modelo != "haiku-rapido" {
		t.Fatalf("GrupoDoUsuario = %+v ok=%v err=%v", got, ok, err)
	}

	// usuário relido traz o grupo.
	relido, err := d.ObterUsuario(ctx, u.ID)
	if err != nil || relido.GrupoID == nil || *relido.GrupoID != g.ID || relido.GrupoNome != "Suporte" {
		t.Fatalf("usuário sem grupo resolvido: %+v err=%v", relido, err)
	}

	// trocar de grupo substitui o vínculo (um grupo por usuário).
	g2, err := d.CriarGrupoUsuarios(ctx, GrupoUsuarios{Nome: "Produto"})
	if err != nil {
		t.Fatalf("segundo grupo: %v", err)
	}
	if err := d.DefinirGrupoDoUsuario(ctx, u.ID, &g2.ID); err != nil {
		t.Fatalf("trocar grupo: %v", err)
	}
	got, _, _ = d.GrupoDoUsuario(ctx, u.ID)
	if got.ID != g2.ID {
		t.Fatalf("grupo após troca = %d, quero %d", got.ID, g2.ID)
	}

	// listagem traz nomes dos membros.
	grupos, err := d.ListarGruposUsuarios(ctx)
	if err != nil {
		t.Fatalf("ListarGruposUsuarios: %v", err)
	}
	for _, gr := range grupos {
		if gr.ID == g2.ID && (len(gr.Usuarios) != 1 || gr.Usuarios[0] != "Ana") {
			t.Fatalf("membros do grupo Produto = %v, quero [Ana]", gr.Usuarios)
		}
	}

	// desvincular.
	if err := d.DefinirGrupoDoUsuario(ctx, u.ID, nil); err != nil {
		t.Fatalf("desvincular: %v", err)
	}
	if _, ok, _ := d.GrupoDoUsuario(ctx, u.ID); ok {
		t.Fatal("usuário ainda com grupo após desvincular")
	}

	// excluir grupo com membro: cascade limpa o vínculo, usuário fica.
	if err := d.DefinirGrupoDoUsuario(ctx, u.ID, &g.ID); err != nil {
		t.Fatalf("revincular: %v", err)
	}
	if err := d.ExcluirGrupoUsuarios(ctx, g.ID); err != nil {
		t.Fatalf("ExcluirGrupoUsuarios: %v", err)
	}
	if _, ok, _ := d.GrupoDoUsuario(ctx, u.ID); ok {
		t.Fatal("vínculo sobreviveu à exclusão do grupo")
	}
	if _, err := d.ObterUsuario(ctx, u.ID); err != nil {
		t.Fatalf("usuário sumiu com o grupo: %v", err)
	}

	// remover o motor não remove o grupo (SET NULL).
	g3, err := d.CriarGrupoUsuarios(ctx, GrupoUsuarios{Nome: "Comercial", EngineID: &motor.ID})
	if err != nil {
		t.Fatalf("terceiro grupo: %v", err)
	}
	if _, err := d.Escritor.ExecContext(ctx, `DELETE FROM engines WHERE id = ?`, motor.ID); err != nil {
		t.Fatalf("remover motor: %v", err)
	}
	got3, err := d.ObterGrupoUsuarios(ctx, g3.ID)
	if err != nil || got3.EngineID != nil {
		t.Fatalf("grupo após remover motor = %+v err=%v, quero engine_id nulo", got3, err)
	}
}
