# Imagem oficial do Praxis Autonomous.
#
# Postura (Fase B do PLANO_MULTIUSUARIO.md): o serviço roda como o usuário
# NÃO-ROOT `praxis` — o claude recusa execução autônoma com UID 0, e um
# orquestrador que roda agentes autônomos não deve ser root de qualquer forma.
#
# Build (da raiz do repositório):
#   docker build -f docker/Dockerfile -t praxis-autonomous .
# Uso rápido:
#   docker run -d -p 7799:7799 -v praxis-home:/home/praxis/.config/praxis \
#     -v /caminho/dos/repos:/repos praxis-autonomous
# Ou: docker compose -f docker/docker-compose.yml up -d

# --- build -------------------------------------------------------------------
FROM golang:1.26-bookworm AS build
WORKDIR /src
# Camada de dependências separada: o cache sobrevive a mudanças no código.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/praxis ./cmd/praxis

# --- runtime -----------------------------------------------------------------
FROM debian:bookworm-slim

# git: exigido pelo Praxis (worktrees, merges). openssh-client: chaves por
# usuário da Fase C (ssh-keygen + clones por SSH). ca-certificates: HTTPS dos
# vendors (APIs dos harnesses, downloads). curl ajuda diagnóstico e instaladores.
RUN apt-get update && apt-get install -y --no-install-recommends \
      git openssh-client ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*

# Usuário de serviço não-root com home de verdade: o harness e o git guardam
# config/caches em $HOME (~/.claude, ~/.gitconfig).
RUN useradd --create-home --user-group --shell /bin/bash praxis

COPY --from=build /out/praxis /usr/local/bin/praxis

USER praxis
WORKDIR /home/praxis
ENV PRAXIS_HOME=/home/praxis/.config/praxis

# Dentro do container, 0.0.0.0: quem publica a porta decide a exposição
# (localhost, VPN ou reverse-proxy com TLS — ver o guia, seção 9).
EXPOSE 7799
VOLUME ["/home/praxis/.config/praxis"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD curl -fsS http://127.0.0.1:7799/healthz || exit 1

ENTRYPOINT ["praxis"]
CMD ["serve", "-addr", "0.0.0.0:7799"]
