// Seletor de pasta do SERVIDOR. O campo `pasta` do projeto é um caminho na
// máquina onde o Praxis roda, não na de quem usa o navegador — por isso o
// <input type="file"> não serve (ele entrega o arquivo, nunca o caminho). Aqui
// a navegação é feita pela API (GET /fs/dirs), que também marca quais pastas já
// são repositórios git.

import { api } from "./api.js";
import { el, limpar } from "./ui.js";
import { t } from "./i18n.js";

// escolherPasta abre o modal de navegação e resolve com o caminho escolhido, ou
// com null se o usuário cancelar. `inicial` é a pasta em que a navegação começa
// (em branco = ponto de partida do servidor).
export function escolherPasta(inicial = "") {
  return new Promise((resolve) => {
    let atual = null; // última resposta de /fs/dirs
    let filtro = "";

    const overlay = el("div", { class: "overlay open" });
    const caminho = el("input", { class: "fs-caminho", value: inicial, spellcheck: "false" });
    const raizes = el("div", { class: "fs-raizes" });
    const busca = el("input", { placeholder: t("seletor_pasta.filtro_ph"), spellcheck: "false" });
    const lista = el("div", { class: "fs-lista" });
    const aviso = el("div", { class: "hint fs-aviso" });
    const btnUsar = el("button", { class: "btn" }, t("seletor_pasta.usar_esta"));

    function fechar(valor) {
      document.removeEventListener("keydown", aoTeclar);
      overlay.remove();
      resolve(valor);
    }
    function aoTeclar(ev) {
      if (ev.key === "Escape") fechar(null);
    }
    document.addEventListener("keydown", aoTeclar);
    overlay.onclick = (ev) => { if (ev.target === overlay) fechar(null); };

    // navegar carrega uma pasta e redesenha a lista. Em erro (pasta inexistente,
    // sem permissão) mantém a listagem anterior e mostra a mensagem do backend.
    async function navegar(destino) {
      limpar(lista).append(el("div", { class: "fs-vazio", text: t("seletor_pasta.carregando") }));
      let dados;
      try {
        dados = await api.listarPastas(destino);
      } catch (e) {
        aviso.textContent = t("seletor_pasta.falha_listar", { erro: e.message });
        aviso.className = "hint fs-aviso erro";
        if (atual) render();
        else limpar(lista);
        return;
      }
      atual = dados;
      filtro = "";
      busca.value = "";
      caminho.value = dados.caminho;
      render();
    }

    // render desenha as raízes, a lista filtrada e o aviso do estado atual.
    function render() {
      limpar(raizes);
      for (const r of atual.raizes || []) {
        raizes.append(el("button", {
          class: "btn ghost sm" + (atual.caminho === r ? " sel" : ""),
          text: r,
          onclick: () => navegar(r),
        }));
      }

      limpar(lista);
      if (atual.pai) {
        lista.append(el("div", { class: "fs-item fs-acima", onclick: () => navegar(atual.pai) },
          el("span", { class: "ico", text: "↰" }),
          el("span", { class: "nm", text: t("seletor_pasta.acima") })));
      }
      const termo = filtro.trim().toLowerCase();
      const pastas = (atual.pastas || []).filter((p) => !termo || p.nome.toLowerCase().includes(termo));
      for (const p of pastas) {
        const item = el("div", { class: "fs-item" + (p.acessivel ? "" : " bloqueado") },
          el("span", { class: "ico", text: p.repo_git ? "◈" : "▸" }),
          el("span", { class: "nm", text: p.nome }),
          p.repo_git ? el("span", { class: "pill", text: t("seletor_pasta.repo") }) : null,
          !p.acessivel ? el("span", { class: "pill", text: t("seletor_pasta.sem_acesso") }) : null,
        );
        if (p.acessivel) {
          item.onclick = () => navegar(p.caminho);
          item.append(el("button", {
            class: "btn sm fs-usar",
            text: t("seletor_pasta.usar"),
            onclick: (ev) => { ev.stopPropagation(); fechar(p.caminho); },
          }));
        }
        lista.append(item);
      }
      if (pastas.length === 0) {
        lista.append(el("div", { class: "fs-vazio", text: termo ? t("seletor_pasta.sem_filtro") : t("seletor_pasta.vazia") }));
      }

      // O aviso conta o que interessa para o cadastro: a pasta aberta é um repo
      // git? (o backend recusa o cadastro de pasta fora de repositório). Uma
      // subpasta de repositório é aceita mesmo sem .git próprio — daí o texto
      // ser um alerta, não um bloqueio.
      if (atual.truncado) {
        aviso.textContent = t("seletor_pasta.truncado");
        aviso.className = "hint fs-aviso erro";
      } else if (atual.repo_git) {
        aviso.textContent = t("seletor_pasta.aviso_repo");
        aviso.className = "hint fs-aviso ok";
      } else {
        aviso.textContent = t("seletor_pasta.aviso_nao_repo");
        aviso.className = "hint fs-aviso";
      }
    }

    busca.oninput = () => { filtro = busca.value; if (atual) render(); };
    caminho.onkeydown = (ev) => { if (ev.key === "Enter") { ev.preventDefault(); navegar(caminho.value.trim()); } };
    btnUsar.onclick = () => fechar(caminho.value.trim());

    overlay.append(el("div", { class: "modal", style: "max-width:680px" },
      el("div", { class: "modal-head" },
        el("div", { class: "row1" },
          el("h2", { text: t("seletor_pasta.titulo") }),
          el("button", { class: "modal-close", text: "✕", onclick: () => fechar(null) }),
        ),
        el("p", { class: "sub", style: "margin:4px 0 12px", text: t("seletor_pasta.sub") }),
      ),
      el("div", { class: "fs-corpo" },
        el("div", { class: "fs-barra" },
          caminho,
          el("button", { class: "btn ghost sm", text: t("seletor_pasta.ir"), onclick: () => navegar(caminho.value.trim()) }),
        ),
        raizes,
        busca,
        lista,
        aviso,
        el("div", { class: "fs-acoes" },
          el("button", { class: "btn ghost", text: t("seletor_pasta.cancelar"), onclick: () => fechar(null) }),
          btnUsar,
        ),
      ),
    ));
    document.body.append(overlay);
    navegar(inicial);
  });
}
