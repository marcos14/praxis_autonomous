package db

import (
	"context"
	"errors"
	"testing"
)

func criarUsuarioTeste(t *testing.T, d *DB, nome string) int64 {
	t.Helper()
	u, err := d.CriarUsuario(context.Background(), nome, nome+"@teste.com", "senha-123", nil)
	if err != nil {
		t.Fatalf("criar usuário %s: %v", nome, err)
	}
	return u.ID
}

func TestProjetoSemACLAbertoATodos(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	proj := criarProjetoTeste(t, d, "aberto")
	uid := criarUsuarioTeste(t, d, "ana")

	ve, err := d.UsuarioVeProjeto(ctx, uid, proj)
	if err != nil || !ve {
		t.Fatalf("UsuarioVeProjeto sem ACL = %v err=%v, quero true", ve, err)
	}
	lista, err := d.ListarProjetosVisiveis(ctx, uid)
	if err != nil || len(lista) != 1 {
		t.Fatalf("ListarProjetosVisiveis = %d projetos err=%v, quero 1", len(lista), err)
	}
	acesso, err := d.ObterAcessoProjeto(ctx, proj)
	if err != nil || acesso.Restrito {
		t.Fatalf("ObterAcessoProjeto = %+v err=%v, quero restrito=false", acesso, err)
	}
}

func TestACLPorUsuarioEGrupo(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	projA := criarProjetoTeste(t, d, "a")
	projB := criarProjetoTeste(t, d, "b")
	ana := criarUsuarioTeste(t, d, "ana")
	beto := criarUsuarioTeste(t, d, "beto")
	caio := criarUsuarioTeste(t, d, "caio")

	grupo, err := d.CriarGrupoUsuarios(ctx, GrupoUsuarios{Nome: "Suporte"})
	if err != nil {
		t.Fatalf("criar grupo: %v", err)
	}
	if err := d.DefinirGrupoDoUsuario(ctx, beto, &grupo.ID); err != nil {
		t.Fatalf("vincular beto ao grupo: %v", err)
	}

	// projA restrito: ana (direto) + grupo Suporte (beto). caio fica de fora.
	if err := d.DefinirAcessoProjeto(ctx, projA, []int64{ana}, []int64{grupo.ID}); err != nil {
		t.Fatalf("DefinirAcessoProjeto: %v", err)
	}

	casos := []struct {
		usuario int64
		quer    bool
	}{{ana, true}, {beto, true}, {caio, false}}
	for _, c := range casos {
		ve, err := d.UsuarioVeProjeto(ctx, c.usuario, projA)
		if err != nil || ve != c.quer {
			t.Fatalf("UsuarioVeProjeto(%d, projA) = %v err=%v, quero %v", c.usuario, ve, err, c.quer)
		}
	}
	// projB sem ACL continua aberto a todos.
	if ve, _ := d.UsuarioVeProjeto(ctx, caio, projB); !ve {
		t.Fatalf("projB sem ACL deveria ser visível a caio")
	}
	lista, err := d.ListarProjetosVisiveis(ctx, caio)
	if err != nil || len(lista) != 1 || lista[0].ID != projB {
		t.Fatalf("ListarProjetosVisiveis(caio) = %+v err=%v, quero só projB", lista, err)
	}

	// Demandas seguem a ACL do projeto.
	dem, err := d.CriarDemanda(ctx, Demanda{ProjectID: projA, Titulo: "d1"})
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}
	if ve, _ := d.UsuarioVeDemanda(ctx, caio, dem.ID); ve {
		t.Fatalf("caio não deveria ver demanda de projA")
	}
	if ve, _ := d.UsuarioVeDemanda(ctx, beto, dem.ID); !ve {
		t.Fatalf("beto (via grupo) deveria ver demanda de projA")
	}
	// Demanda inexistente passa (o handler responde 404).
	if ve, _ := d.UsuarioVeDemanda(ctx, caio, 9999); !ve {
		t.Fatalf("demanda inexistente deveria devolver true (404 no handler)")
	}
	soCaio, err := d.ListarDemandas(ctx, FiltroDemandas{Visao: Visao{ACL: &caio}})
	if err != nil || len(soCaio) != 0 {
		t.Fatalf("ListarDemandas(caio) = %d err=%v, quero 0", len(soCaio), err)
	}
	// beto vê via grupo — pega regressão de coluna não-qualificada no EXISTS.
	deBetoLista, err := d.ListarDemandas(ctx, FiltroDemandas{Visao: Visao{ACL: &beto}})
	if err != nil || len(deBetoLista) != 1 {
		t.Fatalf("ListarDemandas(beto) = %d err=%v, quero 1", len(deBetoLista), err)
	}
	deBeto, err := d.ListarDemandasResumo(ctx, FiltroDemandas{Visao: Visao{ACL: &beto}})
	if err != nil || len(deBeto) != 1 {
		t.Fatalf("ListarDemandasResumo(beto) = %d err=%v, quero 1", len(deBeto), err)
	}

	// Eventos: os do projeto restrito somem para caio; os globais ficam.
	if _, err := d.RegistrarEvento(ctx, Evento{ProjectID: &projA, Tipo: "t", Titulo: "restrito"}); err != nil {
		t.Fatalf("evento projA: %v", err)
	}
	if _, err := d.RegistrarEvento(ctx, Evento{Tipo: "t", Titulo: "global"}); err != nil {
		t.Fatalf("evento global: %v", err)
	}
	evs, err := d.ListarEventos(ctx, FiltroEventos{Visao: Visao{ACL: &caio}})
	if err != nil || len(evs) != 1 || evs[0].Titulo != "global" {
		t.Fatalf("ListarEventos(caio) = %+v err=%v, quero só o global", evs, err)
	}
	evsApos, err := d.EventosApos(ctx, 0, 10, Visao{ACL: &caio})
	if err != nil || len(evsApos) != 1 {
		t.Fatalf("EventosApos(caio) = %d err=%v, quero 1", len(evsApos), err)
	}

	// Grupo de PROJETOS (consultas): com um projeto restrito dentro, some para
	// caio; segue visível para beto (que vê todos os membros).
	pg, err := d.CriarGrupo(ctx, Grupo{Nome: "Solução X", Slug: "solucao-x"}, []int64{projA, projB})
	if err != nil {
		t.Fatalf("criar grupo de projetos: %v", err)
	}
	if ve, _ := d.UsuarioVeGrupoProjetos(ctx, caio, pg.ID); ve {
		t.Fatalf("caio não deveria ver o grupo de projetos com projA restrito")
	}
	if ve, _ := d.UsuarioVeGrupoProjetos(ctx, beto, pg.ID); !ve {
		t.Fatalf("beto deveria ver o grupo de projetos")
	}

	// Substituição da ACL: listas vazias reabrem o projeto.
	if err := d.DefinirAcessoProjeto(ctx, projA, nil, nil); err != nil {
		t.Fatalf("limpar ACL: %v", err)
	}
	if ve, _ := d.UsuarioVeProjeto(ctx, caio, projA); !ve {
		t.Fatalf("projA sem ACL deveria voltar a ser visível a caio")
	}
}

func TestObterAcessoProjetoInexistente(t *testing.T) {
	d := abrirTemp(t)
	if _, err := d.ObterAcessoProjeto(context.Background(), 999); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("ObterAcessoProjeto(999) = %v, quero ErrNaoEncontrado", err)
	}
	if err := d.DefinirAcessoProjeto(context.Background(), 999, nil, nil); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("DefinirAcessoProjeto(999) = %v, quero ErrNaoEncontrado", err)
	}
}
