# ADR 0001 — Serviços instaláveis em Windows e Linux

|  |  |
| :---- | :---- |
| **Status** | Aceita |
| **Data** | 2026-09-05 |
| **Origem** | OpenVisum/VidiAccess (agente de acesso remoto que roda como serviço na frota dos clientes) |
| **Escopo** | Qualquer projeto que precise de um processo residente instalável nas duas plataformas. Os exemplos são em Go, mas **as decisões são independentes de linguagem** — a seção 3 mapeia cada uma para outras stacks. |

## Sumário da decisão

1. **Um único binário com modos** (`run`, `service install`, `service run`, …) — o mesmo executável é o serviço, o instalador e a ferramenta de diagnóstico.  
2. **Falar o protocolo nativo de cada plataforma**: handler do SCM no Windows; processo foreground comum sob `Type=simple` no Linux. Nunca "daemonizar" por conta própria.  
3. **O reinício pertence ao supervisor.** No Linux, `Restart=always` e o processo *sai* quando precisa reiniciar. No Windows, onde reinício automático é opt-in e limitado, o binário orquestra explicitamente (processo auxiliar destacado).  
4. **Consultar estado nunca exige elevação; agir sim.** As chamadas de consulta pedem apenas direitos de leitura ao sistema.  
5. **Estado e configuração em diretórios de máquina** (`%ProgramData%`, `/var/lib`, `/etc`), com caminhos explícitos e idênticos entre instalador, cadastro e serviço.  
6. **O nome do serviço é contrato eterno**: toda consulta e toda ação procuram também os nomes de produtos anteriores; o instalador remove o serviço antigo em vez de conviver com ele.  
7. **Instalador idempotente** que confere o caminho do binário registrado, não só a existência do serviço.  
8. **Logar para onde a plataforma olha**: arquivo próprio (ou Event Log) no Windows; stdout/journald no Linux.  
9. **Detectar o contexto de execução** (SCM × console; terminal × pipe/`/dev/null`) e adaptar interatividade e destino de log.

&nbsp;

---

## 1\. Contexto

### 1.1 O problema

Um agente de frota, um coletor de backup, um sincronizador — qualquer software que precise estar vivo numa máquina o tempo todo — tem cinco obrigações que um programa comum não tem:

&nbsp;

- **subir no boot**, antes de qualquer usuário logar;  
- **sobreviver ao logoff** (não pode morar na sessão de um usuário);  
- **voltar sozinho** depois de um crash ou de uma atualização;  
- **ser administrável** por quem opera a máquina (`iniciar`, `parar`, `status`, `remover`);  
- **registrar o que faz** num lugar que o operador saiba olhar.

&nbsp;

Os dois sistemas resolvem isso com um *supervisor* de processos: o **Service Control Manager (SCM)** no Windows e o **systemd** na esmagadora maioria dos Linux atuais. O erro que mais custa tempo é tratá-los como "a mesma coisa com comandos diferentes". Os modelos divergem em pontos estruturais, e as decisões deste documento existem exatamente para absorver essas divergências:

&nbsp;

| Divergência | Windows (SCM) | Linux (systemd) |
| :---- | :---- | :---- |
| Quem se adapta a quem | **O programa fala um protocolo** com o SCM (senão é morto no start) | O systemd supervisiona **qualquer processo comum** |
| Reinício após falha | Opt-in, configurado à parte, com semântica cheia de pegadinhas | Uma linha no unit (`Restart=always`) |
| Onde vive a configuração de execução | Embutida no registro do serviço (`binPath` com argumentos) | Num arquivo de texto versionável (o unit) |
| Console/stdout | Não existem — o processo nasce sem console | stdout/stderr vão direto para o journal |
| Interação com a tela do usuário | Proibida por isolamento (Session 0\) — exige processo auxiliar | Serviço de sistema não tem display; interação é outro problema |
| Privilégio para consultar estado | Qualquer usuário, **desde que peça só direitos de consulta** | Qualquer usuário |

### 1.2 O modelo do Windows: o SCM

**Registro.** Um serviço é uma entrada no banco do SCM (persistida em `HKLM\SYSTEM\CurrentControlSet\Services\<nome>`) criada por `CreateService`/`sc.exe create`. Ela guarda: o **nome interno** (imutável, usado em toda API), o **nome de exibição**, a descrição, o tipo de início (`Automatic`, `Automatic (Delayed)`, `Manual`, `Disabled`), a conta, e o `binPath` — **a linha de comando completa, argumentos inclusos**. Consequência: os argumentos do serviço ficam congelados na instalação. Regra prática que adotamos: só embutir no `binPath` o que foi pedido explicitamente na instalação, e deixar o resto seguir os defaults do binário — assim uma atualização do executável pode melhorar defaults sem reinstalar o serviço ([modes\_windows.go:190](http://cmd/agent/modes_windows.go#L190)).

&nbsp;

**Protocolo.** O executável iniciado pelo SCM **precisa** se conectar ao dispatcher (`StartServiceCtrlDispatcher`) em \~30 segundos, senão o SCM o mata com o erro 1053\. A partir daí ele mantém uma máquina de estados (`START_PENDING → RUNNING → STOP_PENDING → STOPPED`) e responde a controles (`STOP`, `SHUTDOWN`, `INTERROGATE`, e opcionalmente pause, troca de sessão, energia). O esqueleto completo, em Go ([service\_windows.go:125](http://internal/agent/service_windows.go#L125)):

&nbsp;

func (h \*handler) Execute(args \[\]string, r \<-chan svc.ChangeRequest, s chan\<- svc.Status) (bool, uint32) {

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;s \<- svc.Status{State: svc.StartPending}

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;ctx, cancel := context.WithCancel(context.Background())

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;done := make(chan struct{})

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;go func() { h.run(ctx); close(done) }()

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;s \<- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;for {

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;select {

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;case c := \<-r:

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;switch c.Cmd {

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;case svc.Interrogate:

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;s \<- c.CurrentStatus

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;case svc.Stop, svc.Shutdown:

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;s \<- svc.Status{State: svc.StopPending}

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;cancel()                          // pedido de parada graciosa…

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;select {

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;case \<-done:                      // …com prazo: o SCM não espera

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;case \<-time.After(10 \* time.Second):

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;}

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;return false, 0

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;}

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;case \<-done:

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;return false, 0

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;}

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;}

&nbsp;

}

&nbsp;

Três pontos desse esqueleto valem para qualquer linguagem: reportar `StartPending` **antes** de trabalho demorado; aceitar `Shutdown` além de `Stop` (desligamento da máquina não manda `Stop`); e impor um prazo à parada graciosa — o corpo do serviço recebe um sinal de cancelamento e tem N segundos para drenar.

&nbsp;

**Contas.** O serviço roda sob uma conta escolhida na instalação:

&nbsp;

| Conta | Privilégio | Uso típico |
| :---- | :---- | :---- |
| `LocalSystem` | Máximo (inclui `SeTcbPrivilege`) | Agentes que precisam agir em sessões de usuários, instalar updates |
| `NT AUTHORITY\LocalService` | Mínimo local, anônimo na rede | Serviços sem necessidade de privilégio |
| `NT AUTHORITY\NetworkService` | Mínimo local, identidade da máquina na rede | Acesso a recursos de domínio |
| Conta virtual `NT SERVICE\<nome>` / gMSA | Isolada por serviço | Ambientes de domínio geridos |

&nbsp;

Escolher `LocalSystem` deve ser uma decisão justificada, não o default. No OpenVisum ela é necessária: `WTSQueryUserToken` (criar processo na sessão do usuário) exige `SeTcbPrivilege`, que só `LocalSystem` tem.

&nbsp;

**Session 0\.** Desde o Vista, serviços rodam na sessão 0, isolada de qualquer desktop. Um serviço **não consegue** capturar tela, injetar input ou mostrar janela na sessão do usuário. O padrão para interagir é um **processo auxiliar** (helper): o serviço enumera sessões (`WTSEnumerateSessions`), obtém o token do usuário logado (`WTSQueryUserToken`) e lança o helper naquela sessão com `CreateProcessAsUser` (desktop `winsta0\default`), conversando com ele por um canal local (named pipe). É a arquitetura de [helperproto.go](http://internal/agent/helperproto.go) e [launcher\_windows.go](http://internal/agent/launcher_windows.go). Detalhe que vai além da sessão: mesmo dentro dela, o UIPI (isolamento por nível de integridade) impede que um helper de integridade média envie input a janelas elevadas — interação plena exige o helper lançado com token/integridade adequados, ou seja, exige o serviço.

&nbsp;

**Elevação assimétrica — a lição mais cara deste repositório.** Instalar, iniciar, parar e remover exigem token de administrador (UAC). **Consultar não** — desde que o código peça só direitos de consulta. A API convida ao erro: o caminho "conveniente" (`mgr.Connect()` em Go, e equivalentes em outras stacks) pede `SC_MANAGER_ALL_ACCESS`, que falha sem elevação. O sintoma é traiçoeiro: o programa não elevado conclui "não há serviço" mesmo com o serviço instalado e rodando, e passa a se comportar diferente sob "Executar como administrador". A consulta correta pede o mínimo ([service\_windows.go:57](http://internal/agent/service_windows.go#L57)):

&nbsp;

m, err := windows.OpenSCManager(nil, nil, windows.SC\_MANAGER\_CONNECT) // NÃO ALL\_ACCESS

&nbsp;

// …

&nbsp;

s, err := windows.OpenService(m, nome, windows.SERVICE\_QUERY\_STATUS)  // só query

&nbsp;

// QueryServiceStatus(s, \&st) funciona para qualquer usuário

&nbsp;

**Reinício após falha.** O SCM **não reinicia** um serviço que caiu, por default. As "recovery actions" são configuração à parte (`ChangeServiceConfig2`/`sc.exe failure`) e, por padrão, só disparam quando o processo morre sem reportar `STOPPED` — término com código de saída ≠ 0 só conta como falha se `FailureActionsOnNonCrashFailures` estiver ligado. Exemplo:

&nbsp;

sc.exe failure MeuServico reset= 86400 actions= restart/5000/restart/5000/restart/30000

&nbsp;

sc.exe failureflag MeuServico 1

&nbsp;

(A sintaxe exige o espaço depois de cada `=`.) A decisão D3 existe por causa dessa fragilidade.

&nbsp;

**Sem console.** Processo iniciado pelo SCM não tem console: `printf`/stdout vão para o nada. As opções são o **Event Log** (integrado ao ecossistema do operador Windows, mas hostil a log de alto volume) ou **arquivo próprio** em `%ProgramData%\<Produto>\` — a escolha deste projeto ([modes\_windows.go:113](http://cmd/agent/modes_windows.go#L113)). Com arquivo próprio, a rotação é responsabilidade do programa.

&nbsp;

**Remoção adiada.** `DeleteService` marca para exclusão; o serviço só some quando estiver parado **e** todos os handles abertos (inclusive um `services.msc` esquecido aberto) forem fechados. Instaladores devem parar antes de remover e tolerar o estado "marcado para exclusão".

&nbsp;

**Frotas com Windows antigo.** APIs de sistema aparecem e somem entre versões. Carregamento dinâmico de procedimento ausente (o `LazyProc` do Go, `GetProcAddress` em C) deve ser **sondado antes de chamado** — em Windows Server pré-2016 a chamada direta derruba o processo. Se a frota tem máquinas velhas, todo uso de API "moderna" precisa de probe e fallback.

### 1.3 O modelo do Linux: systemd

**O contrato é invertido.** O systemd supervisiona qualquer processo comum: o programa roda em foreground, escreve em stdout e termina com `exit code`; todo o resto (boot, reinício, logs, dependências) é declarado num **unit file**. Por isso a regra de ouro: **nunca daemonizar por conta própria** (double-fork, `setsid`, pidfile). Isso era necessário no SysV init; sob systemd só atrapalha a supervisão.

&nbsp;

O unit real deste projeto, anotado ([vidiaccess-agent.service](http://internal/packaging/vidiaccess-agent.service)):

&nbsp;

\[Unit\]

&nbsp;

Description=VidiAccess Agent

&nbsp;

After=network-online.target      \# ordena após a rede estar de pé…

&nbsp;

Wants=network-online.target      \# …e pede que esse alvo seja ativado

&nbsp;

\[Service\]

&nbsp;

Type=simple                      \# processo foreground comum

&nbsp;

Environment=RA\_MANAGED=systemd   \# avisa o binário: "há supervisor" (ver D3)

&nbsp;

EnvironmentFile=-/etc/vidiaccess/agent.env   \# "-" \= opcional, não falha se ausente

&nbsp;

WorkingDirectory=/var/lib/vidiaccess

&nbsp;

ExecStart=/usr/local/bin/vidiaccess-agent run \\

&nbsp;

&nbsp;&nbsp;\-id-file /var/lib/vidiaccess/agent-id \\

&nbsp;

&nbsp;&nbsp;\-token-file /var/lib/vidiaccess/agent-token

&nbsp;

Restart=always                   \# o supervisor é dono do reinício

&nbsp;

RestartSec=5

&nbsp;

\[Install\]

&nbsp;

WantedBy=multi-user.target       \# "enable" cria o symlink neste alvo

&nbsp;

Pontos que merecem atenção em qualquer projeto:

&nbsp;

- **`Type=`**: `simple` serve para quase tudo. `notify` (o programa avisa "pronto" via `sd_notify(READY=1)`) vale quando outros units dependem do serviço estar *operante*, não apenas *iniciado*. `forking` existe para daemons legados — não escrever software novo assim. `oneshot` é para tarefas que terminam.  
- **`Restart=` tem freio**: por padrão o systemd desiste após 5 inícios em 10 s (`StartLimitIntervalSec`/`StartLimitBurst`) e o unit entra em `failed`. Um `RestartSec` de alguns segundos evita tropeçar no limite em crash-loop e ainda reinicia rápido.  
- **`network-online.target` não é mágico**: ele só espera a rede se o serviço "wait-online" da distro (NetworkManager-wait-online ou systemd-networkd-wait-online) estiver habilitado. Agente que fala com servidor deve, além disso, **tolerar rede ausente e tentar de novo** — a ordenação de boot é conforto, não garantia.  
- **`enable` ≠ `start`**: `enable` cria o symlink para o boot; `start` sobe agora. E numa **reinstalação**, `enable --now` é armadilha: o serviço já ativo continua rodando **o binário antigo em memória**. O instalador deste projeto usa `enable` \+ `restart` exatamente por isso ([install-linux.sh:108](http://internal/packaging/install-linux.sh#L108)).  
- **Editou o unit → `systemctl daemon-reload`**, senão o systemd segue com a versão em cache.  
- **Parada graciosa**: `stop` manda `SIGTERM`, espera `TimeoutStopSec` (default 90 s) e escala para `SIGKILL`. O programa trata `SIGTERM` como o Windows trata o controle `Stop`: cancela, drena, sai.  
- **Conta e blindagem**: `User=`/`Group=` para não rodar como root sem necessidade; `DynamicUser=yes` \+ `StateDirectory=produto` dão usuário efêmero com estado persistente em `/var/lib/produto`; `AmbientCapabilities=CAP_NET_BIND_SERVICE` permite porta \<1024 sem root; `ProtectSystem=strict`, `ProtectHome=yes`, `PrivateTmp=yes`, `NoNewPrivileges=yes` reduzem a superfície. Serviço que precisa agir na máquina toda (como um agente de suporte) roda como root — de novo: decisão justificada, não default.  
- **Logs**: escrever em stdout/stderr; o journald carimba, indexa e rotaciona (`journalctl -u <unit> -f`). Não inventar arquivo de log próprio no Linux.  
- **stdin é `/dev/null`**: código que pergunta coisas no terminal precisa detectar isso — `/dev/null` é um *character device* como um TTY, então o teste ingênuo por "modo char device" conclui que há um humano e trava o boot esperando resposta ([main.go:126](http://cmd/agent/main.go#L126)).

&nbsp;

**Fora do systemd.** Distros antigas ou enxutas usam SysV init/OpenRC (scripts em `/etc/init.d`, daemonização por conta do programa) e contêineres invertem tudo (o processo É o PID 1; supervisor é o orquestrador). Se a frota incluir esses casos, a estrutura "binário foreground \+ supervisor externo" continua valendo — muda só a peça declarativa.

### 1.4 Tabela de equivalências

Referência rápida para transpor um conceito entre plataformas:

&nbsp;

| Conceito | Windows (SCM) | Linux (systemd) |
| :---- | :---- | :---- |
| Registrar | `CreateService` / `sc.exe create` | copiar unit \+ `systemctl daemon-reload` |
| Iniciar no boot | `StartType = Automatic` | `systemctl enable` (symlink em `WantedBy`) |
| Iniciar / parar agora | `StartService` / controle `Stop` (`sc start/stop`) | `systemctl start` / `stop` |
| Consultar estado | `QueryServiceStatus` (`sc query`) | `systemctl is-active` / `status` |
| Reiniciar após falha | `sc failure` (opt-in, semântica própria) | `Restart=` no unit |
| Parada graciosa | controle `Stop` → `STOP_PENDING` → prazo | `SIGTERM` → `TimeoutStopSec` → `SIGKILL` |
| Argumentos / ambiente | congelados no `binPath` | `ExecStart` \+ `EnvironmentFile` (editáveis) |
| Identidade de execução | conta do serviço (`LocalSystem`, …) | `User=` / `DynamicUser=` |
| Logs | arquivo próprio ou Event Log | stdout → journald |
| Estado da aplicação | `%ProgramData%\<Produto>\` | `/var/lib/<produto>/` (`StateDirectory=`) |
| Configuração | `%ProgramData%` (ou HKLM) | `/etc/<produto>/` |
| Privilégio p/ administrar | Administrador (UAC) | root |
| Privilégio p/ consultar | nenhum (com direitos de query) | nenhum |
| Tela do usuário | proibida (Session 0\) → helper por sessão | sem display; interação é problema à parte |
| Remover | parar \+ `DeleteService` (pode ficar pendente) | `disable --now` \+ apagar unit \+ `daemon-reload` |

### 1.5 O que é igual nas duas plataformas

Quatro invariantes atravessam os modelos e sustentam as decisões da seção 2:

&nbsp;

1. **Identidade da máquina mora em arquivos, e os caminhos são um contrato.** O id e o token que identificam a máquina no servidor ficam no diretório de estado. Instalador, cadastro e serviço **precisam apontar para os mesmos arquivos** — um `-id-file` diferente é, para o servidor, outra máquina. No unit deste projeto os caminhos são explícitos por isso, com comentário dizendo o porquê.  
2. **Renomear o produto não renomeia a frota.** Máquinas instaladas sob o nome antigo continuam com o serviço antigo até alguém reinstalar — ou seja, para sempre. Todo código que consulta ou administra precisa conhecer os nomes legados.  
3. **Instalação acontece mais de uma vez.** Reinstalar, atualizar e reparar são o caso comum, não a exceção. O instalador é idempotente ou é um gerador de chamados.  
4. **Quem foi iniciado pelo supervisor se comporta diferente de quem foi iniciado por um humano.** Sem console/terminal: não perguntar nada, logar para o destino da plataforma, e saber que "reiniciar" significa coisas diferentes (sair × orquestrar).

&nbsp;

---

## 2\. Decisão

### D1 — Um único binário com modos

O mesmo executável é o serviço, o instalador e a ferramenta de linha de comando:

&nbsp;

agent run                  \# foreground (dev, diagnóstico, Linux sob systemd)

&nbsp;

agent service install|remove|start|stop   \# administração (Windows)

&nbsp;

agent service run          \# corpo do serviço (invocado pelo SCM; roda em console p/ debug)

&nbsp;

agent enroll               \# cadastro da máquina

&nbsp;

**Por quê:** elimina desvio de versão entre instalador e serviço; o suporte diagnostica com o binário que já está na máquina; o `binPath` do Windows aponta para o próprio exe com `service run` \+ flags. No Linux o "modo serviço" nem existe como código: é o `run` comum sob o unit. `agent service` em Linux orienta a usar o systemd e sai ([modes\_other.go](http://cmd/agent/modes_other.go)).

### D2 — Falar o protocolo nativo; nunca daemonizar

No Windows, o binário implementa o handler do SCM (§1.2) — sem wrapper. No Linux, o binário roda em foreground e o unit declara o resto (§1.3) — sem double-fork, sem pidfile. O corpo do serviço é **uma função que recebe um contexto de cancelamento**; SCM handler, `SIGTERM` e Ctrl+C do console são três cascas em volta da mesma função. É o que torna o mesmo código executável nos três contextos.

### D3 — O reinício pertence ao supervisor (e onde não há, orquestrar explícito)

O caso que força a decisão é o **auto-update**: o serviço precisa trocar o próprio binário e voltar rodando a versão nova.

&nbsp;

- **Linux:** o unit tem `Restart=always` e `Environment=RA_MANAGED=systemd`. O updater troca o binário e **simplesmente sai**; o systemd sobe a versão nova. Sem a variável (rodando em foreground na mão), o updater se recusa a sair sozinho ([selfupdate\_other.go:17](http://internal/agent/selfupdate_other.go#L17)) — sair sem supervisor seria morrer.  
- **Windows:** o SCM não recompensa "sair e torcer". O updater lança um **processo auxiliar destacado** (`agent service restart-helper`) que sobrevive à parada do serviço: para o serviço, espera parar de fato, e o inicia de novo — subindo o exe recém-trocado ([modes\_windows.go:82](http://cmd/agent/modes_windows.go#L82)). As recovery actions do `sc failure` ficam como rede de segurança para crash, não como mecanismo de update.

&nbsp;

**Regra transportável:** o programa nunca se reinicia "por dentro". Ou sai e o supervisor reinicia, ou um processo externo de vida independente orquestra parada \+ início.

### D4 — Consulta sem elevação; ação com

Toda função de leitura (`está instalado?`, `está rodando?`, `qual binário registrado?`) pede ao SO **apenas direitos de consulta** (`SC_MANAGER_CONNECT` \+ `SERVICE_QUERY_STATUS`/`SERVICE_QUERY_CONFIG`); funciona de qualquer processo. Funções de ação (`install`, `start`, `stop`, `remove`) usam o caminho privilegiado e assumem elevação. **Por quê:** um app de usuário (GUI, tray, verificador) precisa saber o estado do serviço sem UAC; misturar os caminhos produz o bug de §1.2 — comportamento diferente conforme o processo está ou não elevado. Em qualquer linguagem, a pergunta a fazer à API é "quais direitos este handle está pedindo?".

### D5 — Estado em diretórios de máquina, caminhos explícitos e únicos

- Windows: estado e config em `%ProgramData%\<Produto>\` (id, token, log, estado de update).  
- Linux: estado em `/var/lib/<produto>/`, config em `/etc/<produto>/`, binário em `/usr/local/bin/` (ou empacotado).  
- Nunca em perfil de usuário (o serviço roda antes de logins e além deles), nunca "ao lado do exe" (Program Files é read-only para o serviço logar, e instalador rodado do Downloads morre quando o usuário limpa a pasta).  
- Os caminhos aparecem **por extenso** em unit, instalador e código, com o mesmo valor — é um contrato entre as três peças (§1.5.1). Arquivos sensíveis (token, chave de instalação) com permissão `0600`.

### D6 — O nome do serviço é contrato eterno

O nome interno registrado no SO nunca é "o nome atual do produto"; é "o nome sob o qual **esta máquina** registrou". O código mantém a lista de nomes legados e resolve o nome real antes de qualquer ação ([service\_windows.go:32](http://internal/agent/service_windows.go#L32)):

&nbsp;

const ServiceName \= "VidiAccess"

&nbsp;

var legacyServiceNames \= \[\]string{"OpenVisum"} // fica para sempre

&nbsp;

func InstalledServiceName() (string, bool) {

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;if ServiceInstalled(ServiceName) { return ServiceName, true }

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;for \_, n := range legacyServiceNames {

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;if ServiceInstalled(n) { return n, true }

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;}

&nbsp;

&nbsp;&nbsp;&nbsp;&nbsp;return ServiceName, false // instalar usa o nome novo

&nbsp;

}

&nbsp;

O instalador Linux faz o mesmo com o unit antigo — e **remove em vez de conviver**: dois serviços apontando para o mesmo diretório de estado brigariam pela mesma identidade de máquina ([install-linux.sh:99](http://internal/packaging/install-linux.sh#L99)). **Por quê:** sem isso, a consulta "não acha" o serviço legado, o software conclui que não há serviço, e a reinstalação cria um segundo — dois agentes disputando o mesmo id.

### D7 — Instalador idempotente que confere o binário registrado

Rodar o instalador de novo é seguro e é o mecanismo de atualização manual. Além de "o serviço existe?", ele confere **para qual executável o registro aponta** (`ServiceBinaryPath`, [service\_windows.go:99](http://internal/agent/service_windows.go#L99)): serviço apontando para binário antigo, ou para a pasta temporária de onde o instalador foi rodado, é re-registrado, não deixado em paz. No Linux, o equivalente é reinstalar binário \+ unit e usar `restart` (não `enable --now`) para não deixar a versão antiga em memória. Se o cadastro/configuração inicial falhar, o instalador **para com erro antes de subir o serviço** — um serviço sem identidade só ficaria em loop de erro no log ([install-linux.sh:91](http://internal/packaging/install-linux.sh#L91)).

### D8 — Logar para onde a plataforma olha

- Windows: ao detectar início pelo SCM, redirecionar o log para arquivo em `%ProgramData%\<Produto>\` (Event Log para eventos administrativos raros, se houver integração com a operação). Rotação é responsabilidade do programa.  
- Linux: stdout/stderr, sem timestamp próprio redundante — journald resolve carimbo, rotação e consulta.  
- Em ambos: o processo auxiliar/helper que roda fora do supervisor grava o próprio crash em arquivo (`helper-crash.log` neste projeto) — senão a falha some sem rastro.

### D9 — Detectar o contexto de execução e adaptar

Duas detecções, cada uma com sua pegadinha:

&nbsp;

- **"Fui iniciado pelo supervisor?"** Windows: `svc.IsWindowsService()` (a heurística correta olha o processo pai e o desktop da estação — não reimplementar na mão). Decide entre falar o protocolo do SCM ou rodar como console. Linux: a variável `RA_MANAGED` do unit cumpre o papel para o update (D3).  
- **"Há um humano no terminal?"** Decide se pode perguntar (cadastro interativo) ou se deve usar flags e falhar rápido. O teste é "stdin é um TTY **e não é o null device**" — `/dev/null` é char device e é o que o systemd entrega ([main.go:126](http://cmd/agent/main.go#L126)). Pipe (`curl | bash`), redirecionamento e automação também caem no lado "sem humano".

&nbsp;

---

## 3\. Aplicando fora de Go

As decisões acima são de arquitetura; o que muda por stack é quem implementa o protocolo do SCM. **No Linux nada muda**: qualquer runtime roda em foreground sob systemd — as seções §1.3, D3, D5–D9 aplicam-se palavra por palavra.

&nbsp;

No Windows a escolha é **suporte nativo** (a linguagem fala com o SCM) ou **wrapper** (um exe intermediário fala com o SCM e supervisiona o programa):

&nbsp;

| Stack | Caminho recomendado |
| :---- | :---- |
| Go | `golang.org/x/sys/windows/svc` (nativo — este repositório) |
| .NET | `Microsoft.Extensions.Hosting`: `UseWindowsService()` no host; `UseSystemd()` dá `Type=notify` de graça no Linux |
| Rust | crate `windows-service` (nativo) |
| C/C++ | API Win32 direta (`StartServiceCtrlDispatcher` etc.) |
| Java | sem suporte nativo — wrapper: **WinSW** ou Apache Commons Daemon (procrun) |
| Node.js | wrapper (WinSW/NSSM); os pacotes "node-windows" embrulham exatamente isso |
| Python | `pywin32` (`win32serviceutil`) funciona, mas o deploy do runtime pesa — wrapper costuma ser mais simples |

&nbsp;

Ao usar wrapper (WinSW/NSSM), as decisões continuam valendo — aplicadas ao wrapper: o **nome do serviço** registrado é o do wrapper e entra no contrato da D6; o "binPath congelado" vira o XML/config do wrapper (versionar junto do produto); o restart-on-failure é config do wrapper; e o wrapper vira um artefato a mais para distribuir e atualizar. É um custo real: preferir suporte nativo quando a stack o tem maduro.

&nbsp;

Comandos `sc.exe` equivalentes ao que o código deste repo faz por API — úteis para diagnóstico e para stacks sem API:

&nbsp;

sc.exe create MeuServico binPath= "\\"C:\\Program Files\\App\\app.exe\\" service run" start= auto DisplayName= "Meu Produto"

&nbsp;

sc.exe qc MeuServico       & rem config registrada (inclusive o binPath — D7)

&nbsp;

sc.exe query MeuServico    & rem estado (funciona sem elevação)

&nbsp;

sc.exe stop MeuServico & sc.exe delete MeuServico

&nbsp;

---

## 4\. Alternativas consideradas

- **Wrapper no Windows (NSSM/WinSW) em vez de protocolo nativo.** Rejeitado aqui: Go tem suporte maduro e o binário único (D1) perderia a graça com um exe extra para distribuir, versionar e atualizar. É a alternativa certa para stacks sem suporte nativo (§3).  
- **Task Scheduler ("executar no logon/boot") / chave `Run` / pasta Startup.** Rejeitado: sem supervisão, sem estado consultável, sem parada graciosa, sem semântica de reinício; tarefas "no logon" nem rodam sem usuário logado. Serve para utilitários de usuário, não para agentes de máquina.  
- **`cron @reboot` / `rc.local` no Linux.** Rejeitado pelos mesmos motivos — e o processo órfão de supervisor não reinicia após crash.  
- **Daemonização clássica (double-fork \+ pidfile).** Rejeitado: só faria sentido para SysV init puro; sob systemd atrapalha (o `Type=forking` existe para legado, não para código novo).  
- **Serviços de usuário (`systemd --user`, serviços per-user do Windows).** Fora de escopo para agente de máquina: morrem com a sessão e não existem antes do login. São a resposta certa para o problema *diferente* de "processo residente por usuário".  
- **Instalador separado (MSI / deb / rpm).** Não é rival, é camada complementar: empacotamento e distribuição. Um MSI/postinst chamaria exatamente os mesmos comandos (`agent service install`, `systemctl enable`). Este projeto optou por binário auto-instalador \+ script porque a frota é instalada por técnicos de suporte a partir de um ZIP do painel; um projeto com política de pacotes usa a mesma arquitetura por baixo.  
- **macOS (launchd)** ficou fora do escopo desta ADR; a estrutura (foreground \+ plist declarativo \+ `KeepAlive`) é análoga à do systemd e as decisões D1–D9 transpõem direto.

&nbsp;

---

## 5\. Consequências

**Positivas**

&nbsp;

- O mesmo corpo de serviço roda sob SCM, sob systemd, em console e em testes — a diferença entre plataformas fica confinada às "cascas" (handler SCM, unit file, tratador de sinal).  
- Consultas de estado funcionam de qualquer processo, elevado ou não — GUIs e verificadores não precisam de UAC nem de root.  
- Atualização e reinstalação são operações de rotina, não cirurgias: instalador idempotente \+ supervisor dono do reinício.  
- Renomear o produto não quebra a frota instalada.

&nbsp;

**Custos e riscos assumidos**

&nbsp;

- **Código específico por plataforma** (build tags em Go, `#ifdef`/módulos em outras stacks) para as cascas: é o preço de falar os protocolos nativos.  
- **A lista de nomes legados só cresce** e nunca pode ser podada — o teste de qualquer nova consulta precisa cobrir os nomes antigos.  
- **O helper de sessão do Windows é complexidade real** (tokens, pipes, crash fora do supervisor). Só pagar esse preço se o serviço de fato precisa da tela/input do usuário.  
- **Testar exige a plataforma de verdade**: elevação, SCM e Session 0 não se emulam em CI comum — é preciso VM Windows (idealmente incluindo a versão mais velha da frota) e uma VM systemd. O instalador Linux deste projeto contorna parte disso com destinos sobrescrevíveis por ambiente (`CONF_DIR`, `STATE_DIR`…), o que permite exercitá-lo em teste automatizado sem tocar a máquina.  
- **Rotação de log no Windows é nossa** (journald não existe lá).

&nbsp;

---

## 6\. Checklist para um projeto novo

**Desenho**

&nbsp;

- [ ] Corpo do serviço \= função com contexto de cancelamento; cascas por plataforma em volta (D2).  
- [ ] Um binário com modos `run` / administração / corpo do serviço (D1).  
- [ ] Nome interno do serviço escolhido como identificador estável (não marca de fantasia) \+ lista de legados vazia criada desde o dia 1 (D6).  
- [ ] Caminhos de estado/config decididos e escritos por extenso num único lugar (D5).

&nbsp;

**Windows**

&nbsp;

- [ ] Handler do SCM: `StartPending` cedo, `Stop` **e** `Shutdown`, parada com prazo (§1.2).  
- [ ] Consultas com direitos mínimos (`SC_MANAGER_CONNECT` \+ query) — testar **sem** elevação (D4).  
- [ ] Conta do serviço justificada; `LocalSystem` só se precisar (§1.2).  
- [ ] Precisa de tela/input do usuário? → helper por sessão via token do usuário; crash do helper logado em arquivo (§1.2, D8).  
- [ ] `sc failure` como rede de segurança; update reinicia via processo destacado (D3).  
- [ ] Log em arquivo sob `%ProgramData%` quando iniciado pelo SCM, com rotação (D8).  
- [ ] Frota tem Windows antigo? → probe de API antes de chamada dinâmica (§1.2).

&nbsp;

**Linux**

&nbsp;

- [ ] Foreground, `Type=simple` (ou `notify` se dependentes precisam de "pronto"), **sem** daemonizar (§1.3).  
- [ ] `Restart=always` \+ `RestartSec`; variável de ambiente sinalizando "há supervisor" para o updater (D3).  
- [ ] `SIGTERM` tratado como parada graciosa; cabe em `TimeoutStopSec` (§1.3).  
- [ ] Logs em stdout; nada de arquivo próprio (D8).  
- [ ] `User=`/hardening no unit, ou root justificado (§1.3).  
- [ ] Nenhum prompt quando stdin é `/dev/null`/pipe (D9).

&nbsp;

**Instalador (ambos)**

&nbsp;

- [ ] Idempotente: rodar de novo atualiza binário e registro/unit (D7).  
- [ ] Confere o `binPath`/`ExecStart` registrado, não só a existência (D7).  
- [ ] Reinstalação usa *restart*, nunca deixa o binário antigo em memória (D7).  
- [ ] Remove/assume o serviço de nome legado em vez de conviver (D6).  
- [ ] Falha de configuração inicial aborta **antes** de subir o serviço (D7).  
- [ ] Arquivos sensíveis com permissão restrita (D5).

&nbsp;

---

## 7\. Referências

**Neste repositório** — [internal/agent/service\_windows.go](http://internal/agent/service_windows.go) (SCM: handler, consultas sem elevação, nomes legados) · [cmd/agent/modes\_windows.go](http://cmd/agent/modes_windows.go) (modos, instalação, restart-helper) · [cmd/agent/main.go](http://cmd/agent/main.go) (detecção de terminal, cadastro) · [internal/packaging/vidiaccess-agent.service](http://internal/packaging/vidiaccess-agent.service) (unit anotado) · [internal/packaging/install-linux.sh](http://internal/packaging/install-linux.sh) (instalador idempotente) · [internal/agent/helperproto.go](http://internal/agent/helperproto.go) (protocolo serviço ↔ helper de sessão).

&nbsp;

**Plataforma** — Microsoft: *Service Control Manager*, *Writing a ServiceMain function*, *Session 0 Isolation*, `sc.exe` (Learn). systemd: `man systemd.service(5)`, `systemd.unit(5)`, `systemd.exec(5)`, `sd_notify(3)`. Wrappers: WinSW (github.com/winsw/winsw), NSSM (nssm.cc).

&nbsp;