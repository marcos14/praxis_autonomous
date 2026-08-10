package db

import (
	"context"
	"testing"
)

func TestMotorSemACLEhPublico(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	m := criarMotorTeste(t, d, "claude")
	uid := criarUsuarioTeste(t, d, "ana")

	// Visível para um usuário qualquer…
	ids, err := d.IDsMotoresVisiveis(ctx, &uid)
	if err != nil || !ids[m.ID] {
		t.Fatalf("motor público invisível ao usuário: ids=%v err=%v", ids, err)
	}
	// …e para trabalho SEM criador (token de API/bootstrap).
	ids, err = d.IDsMotoresVisiveis(ctx, nil)
	if err != nil || !ids[m.ID] {
		t.Fatalf("motor público invisível sem criador: ids=%v err=%v", ids, err)
	}
	acesso, err := d.ObterAcessoMotor(ctx, m.ID)
	if err != nil || acesso.Restrito {
		t.Fatalf("acesso = %+v err=%v, quero restrito=false", acesso, err)
	}
}

func TestMotorRestritoPorUsuarioEGrupo(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	m := criarMotorTeste(t, d, "restrito")
	dona := criarUsuarioTeste(t, d, "dona")
	colega := criarUsuarioTeste(t, d, "colega")
	fora := criarUsuarioTeste(t, d, "fora")

	grupo, err := d.CriarGrupoUsuarios(ctx, GrupoUsuarios{Nome: "time"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.DefinirGrupoDoUsuario(ctx, colega, &grupo.ID); err != nil {
		t.Fatal(err)
	}

	// ACL: dona (usuário) + time (grupo).
	if err := d.DefinirAcessoMotor(ctx, m.ID, []int64{dona}, []int64{grupo.ID}); err != nil {
		t.Fatalf("definir acesso: %v", err)
	}

	casos := []struct {
		nome string
		uid  *int64
		ve   bool
	}{
		{"dona (linha de usuário)", &dona, true},
		{"colega (pelo grupo)", &colega, true},
		{"fora (sem vínculo)", &fora, false},
		{"sem criador (token)", nil, false},
	}
	for _, c := range casos {
		ids, err := d.IDsMotoresVisiveis(ctx, c.uid)
		if err != nil {
			t.Fatalf("%s: %v", c.nome, err)
		}
		if ids[m.ID] != c.ve {
			t.Errorf("%s: visível=%v, quero %v", c.nome, ids[m.ID], c.ve)
		}
	}

	// MotorVisivelPara segue o mesmo veredito.
	if ve, _ := d.MotorVisivelPara(ctx, m.ID, &fora); ve {
		t.Error("MotorVisivelPara(fora) = true, quero false")
	}
	if ve, _ := d.MotorVisivelPara(ctx, m.ID, &dona); !ve {
		t.Error("MotorVisivelPara(dona) = false, quero true")
	}

	// FiltrarMotoresVisiveis preserva a ordem e filtra.
	publicoID := criarMotorTeste(t, d, "publico").ID
	todos, err := d.ListarMotores(ctx)
	if err != nil {
		t.Fatal(err)
	}
	so, err := d.FiltrarMotoresVisiveis(ctx, todos, &fora)
	if err != nil {
		t.Fatal(err)
	}
	if len(so) != 1 || so[0].ID != publicoID {
		t.Fatalf("filtrado para 'fora' = %v, quero só o motor público", so)
	}
}

func TestDecoracaoDeVisibilidadeDosMotores(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	dona := criarUsuarioTeste(t, d, "dona")
	grupo, err := d.CriarGrupoUsuarios(ctx, GrupoUsuarios{Nome: "time"})
	if err != nil {
		t.Fatal(err)
	}

	_ = criarMotorTeste(t, d, "publico")
	privado, err := d.CriarMotor(ctx, Motor{Nome: "privado", Ativo: true, OwnerUserID: &dona})
	if err != nil {
		t.Fatal(err)
	}
	deGrupo := criarMotorTeste(t, d, "de-grupo")
	if err := d.DefinirAcessoMotor(ctx, privado.ID, []int64{dona}, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.DefinirAcessoMotor(ctx, deGrupo.ID, nil, []int64{grupo.ID}); err != nil {
		t.Fatal(err)
	}

	motores, err := d.ListarMotores(ctx)
	if err != nil {
		t.Fatal(err)
	}
	porNome := map[string]Motor{}
	for _, m := range motores {
		porNome[m.Nome] = m
	}
	if v := porNome["publico"].Visibilidade; v != VisibilidadePublica {
		t.Errorf("publico.visibilidade = %q", v)
	}
	if v := porNome["privado"].Visibilidade; v != VisibilidadePrivada {
		t.Errorf("privado.visibilidade = %q", v)
	}
	if porNome["privado"].DonoNome != "dona" {
		t.Errorf("privado.dono_nome = %q, quero dona", porNome["privado"].DonoNome)
	}
	if v := porNome["de-grupo"].Visibilidade; v != VisibilidadeGrupo {
		t.Errorf("de-grupo.visibilidade = %q", v)
	}
	if g := porNome["de-grupo"].GrupoID; g == nil || *g != grupo.ID {
		t.Errorf("de-grupo.grupo_id = %v, quero %d", g, grupo.ID)
	}

	// Remover o grupo (CASCADE) devolve o motor ao público — semântica documentada.
	if err := d.ExcluirGrupoUsuarios(ctx, grupo.ID); err != nil {
		t.Fatal(err)
	}
	depois, err := d.ObterMotor(ctx, deGrupo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if depois.Visibilidade != VisibilidadePublica {
		t.Errorf("após remover o grupo: visibilidade = %q, quero publica", depois.Visibilidade)
	}
}

func TestDonoDeProjetoDecorado(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	dona := criarUsuarioTeste(t, d, "dona")

	p, err := d.CriarProjeto(ctx, Projeto{
		Nome: "Meu", Slug: "meu", Pasta: t.TempDir(), BranchPrincipal: "main",
		ModoIntegracao: ModoIntegracaoMergeRequest, AddDirs: []string{}, Ativo: true,
		OwnerUserID: &dona,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.DefinirAcessoProjeto(ctx, p.ID, []int64{dona}, nil); err != nil {
		t.Fatal(err)
	}
	lido, err := d.ObterProjeto(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lido.OwnerUserID == nil || *lido.OwnerUserID != dona || lido.DonoNome != "dona" {
		t.Fatalf("dono = %v/%q, quero %d/dona", lido.OwnerUserID, lido.DonoNome, dona)
	}
	if lido.Visibilidade != VisibilidadePrivada {
		t.Fatalf("visibilidade = %q, quero privada", lido.Visibilidade)
	}
}
