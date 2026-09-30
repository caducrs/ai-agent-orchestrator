# Requirements Document

## Introduction

O **AI_Agent_Orchestrator** é uma plataforma profissional de portfólio que recebe uma tarefa complexa em linguagem natural, cria um plano executável, seleciona agentes especializados, executa subtarefas de modo concorrente, usa ferramentas reais sob políticas de segurança, agrega os resultados com um modelo de linguagem e disponibiliza progresso e histórico. A primeira versão deve demonstrar concorrência idiomática em Go, processamento assíncrono durável, segurança em profundidade, observabilidade e recuperação de falhas sem introduzir camadas arquiteturais sem benefício mensurável.

Este documento define somente necessidades e comportamentos verificáveis. Arquitetura, componentes, modelo de dados físico, fluxos detalhados, escolha de mensageria, riscos técnicos e roadmap permanecem objetivos da fase de design posterior.

### Escopo da primeira versão

- API REST versionada para criar, consultar e cancelar tarefas, acompanhar eventos, consultar agentes e verificar saúde.
- Planejamento e agregação assistidos por um **Provider_LLM** compatível com a API da OpenAI.
- Quatro especializações: logs, banco de dados, código e infraestrutura.
- Ferramentas reais e somente leitura, com pelo menos um conector executável por especialização.
- Execução concorrente, assíncrona, limitada e recuperável.
- PostgreSQL como fonte durável de verdade e Redis para coordenação efêmera, cache e rate limiting.
- Telemetria com OpenTelemetry e logs estruturados.
- Ambiente local reproduzível com Docker Compose e suíte automatizada de testes.

### Fora do escopo da primeira versão

- Interface gráfica.
- Ferramentas de escrita, remediação automática, execução arbitrária de shell ou alteração de infraestrutura.
- Treinamento ou fine-tuning de modelos.
- Marketplace, criação dinâmica de tipos de agentes ou cobrança multi-tenant.
- Alta disponibilidade multi-região e garantia de processamento exatamente uma vez.
- Busca paginada do histórico sem um `Task_ID`; a persistência e a consulta por identificador permanecem no escopo.

## Glossary

- **AI_Agent_Orchestrator**: sistema completo descrito neste documento.
- **API_Service**: fronteira HTTP versionada do AI_Agent_Orchestrator.
- **Orchestrator**: componente lógico que coordena planejamento, despacho, execução, agregação e ciclo de vida de uma Task.
- **Planner**: componente lógico que transforma uma Task_Description em um Execution_Plan validado.
- **Scheduler**: componente lógico que identifica Subtasks prontas e aplica dependências, prioridades, justiça entre Tasks e limites de concorrência.
- **Aggregator**: componente lógico que combina Normalized_Results em um Final_Result.
- **Agent_Registry**: catálogo dos Specialized_Agents, capacidades, estado operacional e ferramentas permitidas.
- **Agent_Selection_Policy**: regra configurada e determinística para escolher um Specialized_Agent entre candidatos elegíveis.
- **Agent_Assignment**: vínculo lógico entre um Attempt de Subtask e exatamente um Specialized_Agent selecionado.
- **Assignment_ID**: identificador idempotente de um Agent_Assignment.
- **Specialized_Agent**: executor especializado que recebe uma Subtask e pode propor Tool_Calls dentro de uma capacidade declarada.
- **Log_Agent**: Specialized_Agent para busca e agregação de logs.
- **Database_Agent**: Specialized_Agent para inspeção de metadados e consultas somente leitura.
- **Code_Analysis_Agent**: Specialized_Agent para busca e leitura de código e inspeção de dependências.
- **Infrastructure_Agent**: Specialized_Agent para leitura de saúde e métricas de infraestrutura.
- **Provider_LLM**: serviço de modelo de linguagem compatível com os contratos configurados da API da OpenAI.
- **LLM_Gateway**: fronteira controlada para chamadas ao Provider_LLM, incluindo orçamento, timeout, retry e validação.
- **Structured_LLM_Response**: resposta do Provider_LLM que obedece a um JSON_Schema versionado.
- **LLM_Response_Parser**: componente que converte uma Structured_LLM_Response em uma estrutura interna validada.
- **LLM_Response_Printer**: componente que serializa a estrutura interna validada no formato canônico do mesmo JSON_Schema.
- **Task**: solicitação assíncrona submetida por um Principal.
- **Task_Description**: texto UTF-8 em linguagem natural que descreve o objetivo da Task.
- **Task_ID**: identificador opaco e globalmente único de uma Task.
- **Task_Record**: representação durável de uma Task, incluindo proprietário, estado, prazos e referências de auditoria.
- **Task_Status**: um dos estados `QUEUED`, `PLANNING`, `RUNNING`, `AGGREGATING`, `COMPLETED`, `PARTIALLY_COMPLETED`, `FAILED` ou `CANCELLED`.
- **Terminal_Task_Status**: um dos estados `COMPLETED`, `PARTIALLY_COMPLETED`, `FAILED` ou `CANCELLED`.
- **Task_State_Machine**: regras que controlam transições de Task_Status.
- **Transition_ID**: identificador idempotente de uma solicitação de transição de estado.
- **Execution_Plan**: grafo acíclico dirigido e limitado de Subtasks e Dependencies produzido durante `PLANNING`.
- **DAG_Closure**: condição em que todas as Subtasks de um Execution_Plan estão em estado terminal e nenhuma transição dependente permanece pendente.
- **Subtask**: unidade de trabalho do Execution_Plan atribuível a uma capacidade especializada.
- **Subtask_ID**: identificador único de uma Subtask dentro de uma Task.
- **Subtask_Status**: um dos estados `PENDING`, `BLOCKED`, `READY`, `RUNNING`, `SUCCEEDED`, `FAILED`, `SKIPPED` ou `CANCELLED`.
- **Dependency**: relação em que uma Subtask sucessora depende do resultado de uma Subtask predecessora.
- **Attempt**: uma execução numerada de uma Subtask, Tool_Call ou chamada ao Provider_LLM.
- **Attempt_ID**: identificador idempotente de um Attempt.
- **Normalized_Result**: saída estruturada de uma Subtask, contendo dados, resumo, Source_References, avisos e erro classificado.
- **Useful_Result**: Normalized_Result bem-sucedido que contém pelo menos uma evidência ou conclusão utilizável pelo Aggregator.
- **Late_Result**: resultado recebido depois de a operação ou Task correspondente ter alcançado estado que impede a incorporação do resultado.
- **Final_Result**: resposta final estruturada e narrativa da Task, com conclusões, evidências, lacunas e falhas parciais.
- **Source_Reference**: referência rastreável ao agente, ferramenta, fonte, intervalo ou artefato que sustenta uma conclusão.
- **Tool**: operação externa ou local registrada que um Specialized_Agent pode solicitar.
- **Real_Tool**: Tool que realiza I/O contra uma fonte configurada real; respostas fixas e mocks são permitidos apenas em testes.
- **Tool_Call**: solicitação estruturada para executar uma Tool.
- **Tool_Call_ID**: identificador idempotente de uma Tool_Call.
- **Canonical_Tool_Request**: representação normalizada e validada de Tool, argumentos, alvo lógico e identidade usada para autorização, idempotência e auditoria.
- **Tool_Gateway**: ponto de controle que autoriza, valida, limita, executa e audita Tool_Calls.
- **Tool_Policy**: conjunto configurado de allowlists, limites, permissões e regras de uma Tool.
- **Tool_Output**: dados retornados por uma Tool e tratados como conteúdo não confiável.
- **SQL_Query_Validator**: validador do subconjunto somente leitura da gramática SQL configurada do PostgreSQL.
- **Read_Only_Query**: representação interna de uma única consulta aceita pelo SQL_Query_Validator.
- **SQL_Query_Printer**: serializador canônico de uma Read_Only_Query.
- **AST**: árvore sintática abstrata que representa a estrutura de uma Read_Only_Query.
- **Principal**: identidade humana ou de serviço autenticada.
- **Authorization_Credential**: credencial bearer verificável emitida por uma autoridade configurada.
- **Role**: conjunto nomeado de permissões.
- **Scope**: permissão granular associada a uma ação da API ou Tool.
- **Owner**: Principal que criou uma Task.
- **Single_Organization_Mode**: modo inicial em que uma implantação atende uma organização e ainda aplica propriedade por Principal.
- **Authentication_Service**: componente que valida Authorization_Credentials.
- **Authorization_Service**: componente que decide acesso por Role, Scope, propriedade e Tool_Policy.
- **Authorization_Decision**: decisão indivisível de permitir ou negar uma operação depois de avaliar todas as condições obrigatórias definidas pela política vigente.
- **Rate_Limiter**: componente que aplica limites de uso por Principal, origem e operação.
- **Secret_Manager**: fonte configurada de segredos em tempo de execução.
- **Idempotency_Key**: chave fornecida pelo cliente para identificar submissões repetidas da mesma operação.
- **Idempotency_Record**: registro durável que associa Principal, Idempotency_Key, Request_Fingerprint, expiração e resultado original.
- **Request_Fingerprint**: representação estável dos campos de requisição definidos pelo JSON_Schema e usada para detectar repetição e conflito.
- **API_Payload_Size**: quantidade de bytes do corpo da requisição após decodificações de transferência e conteúdo declaradas e suportadas pelo contrato HTTP e antes da análise do JSON, calculada pelos bytes efetivamente recebidos.
- **JSON_Schema**: contrato JSON versionado usado para validar payloads de API, respostas do Provider_LLM e argumentos de Tool_Calls.
- **Error_Envelope**: resposta JSON versionada com código estável, mensagem sem detalhes internos ou dados sensíveis, detalhes de campos e Trace_ID.
- **Worker_Pool**: conjunto limitado de goroutines reutilizáveis que consomem trabalho de uma Bounded_Queue.
- **Bounded_Queue**: fila com capacidade finita implementada dentro do processo ou por um Message_Backbone.
- **Backpressure**: redução ou rejeição explícita de entrada quando a capacidade configurada está ocupada.
- **Execution_Context**: `context.Context` de Go que transporta cancelamento, prazo e Trace_Context.
- **Message_Backbone**: tecnologia opcional de mensageria durável usada pelo Asynchronous_Dispatch.
- **Asynchronous_Dispatch**: mecanismo que entrega trabalho persistido a consumidores sem bloquear a requisição HTTP original.
- **Delivery_Attempt**: tentativa identificável de entregar uma mensagem a um consumidor.
- **Acknowledgement**: confirmação de processamento aceita somente para a entrega e o Lease correspondentes.
- **Dead_Letter_Record**: registro durável de trabalho que excedeu a política de entrega ou falhou permanentemente.
- **PostgreSQL_Store**: PostgreSQL usado como fonte durável de verdade.
- **Redis_Service**: Redis usado para coordenação efêmera, cache ou rate limiting, sem ser a única cópia de estado de negócio.
- **Task_Event**: registro imutável de uma mudança ou progresso de Task, Subtask, Attempt ou Tool_Call.
- **Event_ID**: identificador monotônico dentro de uma Task e estável entre retransmissões.
- **Event_Stream**: canal unidirecional autenticado do servidor para o cliente em `GET /api/v1/tasks/{id}/events`.
- **Resume_Cursor**: último Event_ID confirmado pelo cliente para retomar um Event_Stream.
- **Real_Time**: disponibilidade de um Task_Event para um cliente conectado em até 2 segundos após a confirmação durável do evento, no percentil 95 e enquanto o sistema estiver dentro dos limites configurados.
- **Retry_Policy**: número máximo de tentativas, atraso exponencial limitado e jitter configurados para uma operação idempotente.
- **Transient_Failure**: falha classificada como recuperável dentro de uma Retry_Policy, como indisponibilidade temporária ou limitação de taxa remota.
- **Permanent_Failure**: falha classificada como não recuperável pela repetição da mesma entrada, como argumento inválido ou autorização negada.
- **Grace_Period**: intervalo finito configurado para cancelamento cooperativo ou encerramento do processo.
- **Trace_Context**: metadados OpenTelemetry que correlacionam operações distribuídas e concorrentes.
- **Trace_ID**: identificador de uma execução de trace.
- **Structured_Log**: registro de log em formato estruturado e pesquisável.
- **Observability_Service**: emissão de traces, métricas e Structured_Logs com OpenTelemetry.
- **Liveness**: indicação de que o processo consegue responder.
- **Readiness**: indicação de que o processo aceita novas Tasks com as dependências obrigatórias disponíveis.
- **Shutdown_Manager**: componente que coordena Graceful_Shutdown.
- **Graceful_Shutdown**: encerramento limitado que interrompe admissão, drena ou torna recuperável o trabalho e fecha recursos.
- **Reference_Limits**: conjunto configurado e validado de capacidades, incluindo tamanhos, concorrência, filas, prazos, tentativas, tokens e Tool_Calls.
- **Decision_Record**: artefato da fase de design que compara alternativas por critérios e registra uma escolha justificável.
- **Docker_Compose_Environment**: ambiente local reproduzível iniciado com Docker Compose.
- **Testcontainers**: biblioteca que inicia dependências reais descartáveis durante testes de integração.
- **Race_Detector**: detector de data races executado por `go test -race`.
- **Local_Development_Mode**: configuração restrita ao ambiente local que usa credenciais e fontes de demonstração sem habilitar bypass silencioso em outros ambientes.
- **Subtask_State_Machine**: regras que controlam transições de Subtask_Status.
- **HTTP**: protocolo de transporte usado pela API REST.
- **REST**: estilo de API baseado em recursos e semântica HTTP.
- **SSE**: Server-Sent Events, protocolo HTTP unidirecional de eventos servidor-cliente.
- **WebSocket**: protocolo de conexão persistente e bidirecional entre cliente e servidor.
- **OIDC**: OpenID Connect, protocolo de identidade usado pela autoridade de autenticação proposta.
- **TLS**: protocolo de proteção de conexões de rede com autenticação e criptografia em trânsito.
- **UTC**: referência de tempo universal usada em timestamps persistidos e públicos.
- **UTF-8**: codificação de caracteres aceita para textos da API.
- **KiB**: unidade de 1.024 bytes.
- **OpenTelemetry**: padrão e conjunto de APIs para traces, métricas e logs correlacionados.
- **Redis Streams**: estrutura de log de mensagens do Redis com consumer groups e acknowledgements.
- **NATS com JetStream**: NATS com a camada persistente JetStream para streams, consumidores e redelivery.
- **RabbitMQ**: broker de mensagens com filas, acknowledgements e roteamento.
- **Lease**: concessão temporária e expirável de propriedade sobre uma unidade de trabalho.
- **Allowlist**: conjunto explícito de valores e operações permitidos por política.

## Premissas propostas para aprovação

1. A primeira versão opera em Single_Organization_Mode; multi-tenancy forte fica fora do escopo.
2. O Event_Stream é somente servidor-cliente; comandos continuam na API REST.
3. Todas as Tools da primeira versão são somente leitura e dispensam aprovação humana interativa depois da autorização por política.
4. A API pública inicial contém somente os seis endpoints solicitados; descoberta paginada do histórico fica adiada.
5. Pelo menos uma fonte real, local ou remota, será configurável para cada tipo de agente; a fonte concreta de demonstração será escolhida no design.
6. Tempos de retenção, cotas e capacidade são configurações obrigatórias; os valores padrão serão aprovados antes da implementação.

## Requirements

### Requirement 1: Submissão assíncrona de tarefas

**User Story:** Como operador autenticado, quero submeter uma tarefa complexa sem aguardar a execução completa, para acompanhar o processamento assíncrono por identificador.

#### Acceptance Criteria

1. THE API_Service SHALL calcular a API_Payload_Size pelos bytes efetivamente recebidos, independentemente do valor declarado em `Content-Length`.
2. WHEN um Principal autorizado enviar `POST /api/v1/tasks` com JSON válido, Task_Description UTF-8 de 1 a 20.000 caracteres e API_Payload_Size de até 256 KiB, THE API_Service SHALL criar uma Task com Task_ID globalmente único.
3. IF o corpo contiver JSON malformado, documentos JSON adicionais, tipo de campo incompatível ou estrutura diferente do JSON_Schema da requisição, THEN THE API_Service SHALL responder com HTTP `400` e um Error_Envelope que identifica a violação sem ecoar conteúdo sensível.
4. IF a Task_Description estiver ausente, vazia ou acima de 20.000 caracteres, THEN THE API_Service SHALL responder com HTTP `400` e um Error_Envelope que identifica o campo inválido.
5. IF a API_Payload_Size exceder 256 KiB durante a leitura, THEN THE API_Service SHALL interromper o processamento da submissão e responder com HTTP `413` e um Error_Envelope.
6. IF o tipo de conteúdo não for `application/json`, THEN THE API_Service SHALL responder com HTTP `415` e um Error_Envelope.
7. WHEN uma Task for criada, THE API_Service SHALL persistir o Task_Record com Task_Status `QUEUED` antes de confirmar aceitação ao cliente.
8. WHEN uma Idempotency_Key acompanhar uma nova submissão válida, THE API_Service SHALL persistir o Idempotency_Record e o Task_Record como uma única mudança consistente.
9. WHEN a persistência do Task_Record for confirmada, THE API_Service SHALL responder com HTTP `202`, Task_ID, Task_Status e horário de aceitação em UTC.
10. IF a persistência obrigatória para a submissão falhar, THEN THE API_Service SHALL responder com HTTP `503` e um Error_Envelope sem representar a Task como aceita.
11. WHEN um cliente repetir a mesma Idempotency_Key, o mesmo Principal e o mesmo Request_Fingerprint dentro de 24 horas, THE API_Service SHALL retornar a resposta associada à submissão original sem criar outra Task.
12. IF um cliente reutilizar a mesma Idempotency_Key e o mesmo Principal com Request_Fingerprint diferente dentro de 24 horas, THEN THE API_Service SHALL responder com HTTP `409` e um Error_Envelope.
13. WHEN submissões concorrentes usarem a mesma Idempotency_Key, o mesmo Principal e o mesmo Request_Fingerprint dentro de 24 horas, THE API_Service SHALL associar todas as respostas bem-sucedidas ao mesmo Task_ID.
14. WHEN o Idempotency_Record de uma Idempotency_Key estiver expirado, THE API_Service SHALL tratar a próxima submissão válida como uma nova operação.
15. THE API_Service SHALL validar os payloads de entrada e saída por JSON_Schemas versionados.

### Requirement 2: Ciclo de vida, consulta e histórico

**User Story:** Como Owner, quero consultar o estado e o histórico de uma Task, para entender o progresso e o resultado persistido.

#### Acceptance Criteria

1. THE Task_State_Machine SHALL aceitar somente as transições de Task_Status definidas na Tabela 1.
2. THE Subtask_State_Machine SHALL aceitar somente as transições de Subtask_Status definidas na Tabela 2.
3. WHEN uma transição de Task_Status for confirmada, THE PostgreSQL_Store SHALL persistir o novo estado, o Transition_ID e o Task_Event correspondente como uma única mudança consistente.
4. WHEN uma transição de Subtask_Status for confirmada, THE PostgreSQL_Store SHALL persistir o novo estado, o Transition_ID e o Task_Event correspondente como uma única mudança consistente.
5. WHEN a mesma solicitação com o mesmo Transition_ID for repetida, THE Task_State_Machine SHALL retornar a transição confirmada sem criar outro Task_Event.
6. WHEN a mesma solicitação com o mesmo Transition_ID for repetida, THE Subtask_State_Machine SHALL retornar a transição confirmada sem criar outro Task_Event.
7. IF um Transition_ID for reutilizado para entidade, origem ou destino diferente, THEN THE AI_Agent_Orchestrator SHALL rejeitar a solicitação com um código de conflito estável.
8. WHEN solicitações concorrentes tentarem transições diferentes a partir da mesma versão de estado, THE PostgreSQL_Store SHALL confirmar no máximo uma transição a partir dessa versão.
9. IF uma solicitação concorrente perder a disputa de transição, THEN THE Task_State_Machine SHALL reavaliar o estado confirmado e retornar o resultado idempotente ou `STATE_TRANSITION_CONFLICT`.
10. IF uma solicitação concorrente de Subtask perder a disputa de transição, THEN THE Subtask_State_Machine SHALL reavaliar o estado confirmado e retornar o resultado idempotente ou `SUBTASK_STATE_TRANSITION_CONFLICT`.
11. WHILE uma Task estiver em Terminal_Task_Status, THE Task_State_Machine SHALL retornar o estado existente para uma repetição idempotente da mesma transição.
12. IF uma transição diferente for solicitada para uma Task em Terminal_Task_Status, THEN THE Task_State_Machine SHALL rejeitar a transição com o código `STATE_TRANSITION_CONFLICT`.
13. WHILE uma Subtask estiver em `SUCCEEDED`, `FAILED`, `SKIPPED` ou `CANCELLED`, THE Subtask_State_Machine SHALL retornar o estado existente para uma repetição idempotente da mesma transição.
14. IF uma transição diferente for solicitada para uma Subtask em estado terminal, THEN THE Subtask_State_Machine SHALL rejeitar a transição com o código `SUBTASK_STATE_TRANSITION_CONFLICT`.
15. WHEN um Principal autorizado solicitar `GET /api/v1/tasks/{id}`, THE API_Service SHALL retornar o Task_Record, o resumo do Execution_Plan, os estados das Subtasks, os Attempts, o Final_Result disponível e os erros classificados.
16. IF o Task_ID não existir ou não estiver visível ao Principal, THEN THE API_Service SHALL responder com HTTP `404` e um Error_Envelope sem revelar a causa entre inexistência e falta de acesso.
17. THE PostgreSQL_Store SHALL preservar Task_Description, Execution_Plan, transições, Attempts, Tool_Calls auditáveis, Task_Events e Final_Result durante o período de retenção configurado.
18. WHEN o período de retenção de uma Task expirar, THE PostgreSQL_Store SHALL aplicar a ação configurada `DELETE` ou `ANONYMIZE` a todos os registros relacionados e registrar a operação de retenção.
19. IF uma ação de retenção falhar parcialmente, THEN THE PostgreSQL_Store SHALL manter a operação recuperável e impedir que o histórico seja apresentado como integralmente retido ou integralmente removido.

**Tabela 1 — Transições permitidas de Task_Status**

| Origem | Destinos permitidos |
|---|---|
| `QUEUED` | `PLANNING`, `CANCELLED` |
| `PLANNING` | `RUNNING`, `FAILED`, `CANCELLED` |
| `RUNNING` | `AGGREGATING`, `FAILED`, `CANCELLED` |
| `AGGREGATING` | `COMPLETED`, `PARTIALLY_COMPLETED`, `FAILED`, `CANCELLED` |
| Estado terminal | repetição idempotente do mesmo estado |

**Tabela 2 — Transições permitidas de Subtask_Status**

| Origem | Destinos permitidos |
|---|---|
| `PENDING` | `BLOCKED`, `READY`, `CANCELLED` |
| `BLOCKED` | `READY`, `SKIPPED`, `CANCELLED` |
| `READY` | `RUNNING`, `SKIPPED`, `CANCELLED` |
| `RUNNING` | `READY`, `SUCCEEDED`, `FAILED`, `CANCELLED` |
| Estado terminal | repetição idempotente do mesmo estado |

### Requirement 3: Planejamento e decomposição validados

**User Story:** Como Owner, quero que uma tarefa complexa seja decomposta em trabalho verificável, para aproveitar agentes especializados com dependências explícitas.

#### Acceptance Criteria

1. WHEN uma Task entrar em `PLANNING`, THE Planner SHALL produzir um Execution_Plan a partir da Task_Description persistida conforme o JSON_Schema versionado de planejamento.
2. THE Planner SHALL atribuir a cada Subtask uma descrição, uma capacidade requerida, Dependencies, timeout, limite de Attempts e JSON_Schema de saída.
3. THE Planner SHALL atribuir a cada Subtask um Subtask_ID único dentro da Task.
4. THE Planner SHALL limitar a quantidade de Subtasks entre 1 e o máximo definido nos Reference_Limits.
5. THE Planner SHALL aplicar os Reference_Limits configurados ao tamanho do Execution_Plan, às Dependencies e aos campos de cada Subtask.
6. WHEN o Planner receber uma Structured_LLM_Response, THE LLM_Response_Parser SHALL validar sintaxe, versão de schema, tipos, campos obrigatórios e limites antes de criar o Execution_Plan.
7. WHEN o LLM_Response_Parser analisar Dependencies, THE LLM_Response_Parser SHALL validar unicidade, existência dos Subtask_IDs referenciados e ausência de autorreferência.
8. WHEN o LLM_Response_Parser concluir a validação estrutural, THE Planner SHALL verificar que o Execution_Plan forma um grafo acíclico dirigido.
9. IF o Execution_Plan contiver Subtask_ID duplicado, ciclo, referência inexistente, autorreferência, schema incompatível ou limite excedido, THEN THE Planner SHALL rejeitar o plano com um erro de validação classificado.
10. IF a resposta de planejamento for inválida e restarem Attempts na Retry_Policy, THEN THE Planner SHALL solicitar uma correção estruturada ao Provider_LLM sem persistir o plano inválido.
11. IF a resposta de planejamento permanecer inválida após a Retry_Policy, THEN THE Orchestrator SHALL mover a Task para `FAILED` com o código `PLAN_INVALID`.
12. WHEN um Execution_Plan válido estiver pronto, THE PostgreSQL_Store SHALL persistir o plano, a versão do JSON_Schema e as Subtasks como uma única mudança consistente.
13. IF a persistência do Execution_Plan falhar e a Retry_Policy permitir outro Attempt, THEN THE Orchestrator SHALL manter a Task em `PLANNING` e aplicar a Retry_Policy classificada.
14. IF o Execution_Plan não puder ser persistido após esgotar a Retry_Policy configurada para persistência, THEN THE Orchestrator SHALL mover a Task para `FAILED` com o código `PLAN_PERSISTENCE_FAILED`.
15. WHEN a persistência do Execution_Plan válido for confirmada, THE Orchestrator SHALL mover a Task de `PLANNING` para `RUNNING`.
16. THE LLM_Response_Printer SHALL serializar um Execution_Plan válido no formato canônico do JSON_Schema de planejamento.
17. WHEN um Execution_Plan válido for impresso, analisado, impresso e analisado novamente, THE LLM_Response_Parser SHALL produzir estruturas internas semanticamente equivalentes nas duas análises.

### Requirement 4: Registro e seleção de agentes

**User Story:** Como Orchestrator, quero selecionar agentes por capacidade e disponibilidade, para encaminhar cada Subtask a um executor compatível.

#### Acceptance Criteria

1. THE Agent_Registry SHALL registrar Log_Agent, Database_Agent, Code_Analysis_Agent e Infrastructure_Agent com identificador, capacidades, versão, estado operacional e Tools permitidas.
2. WHEN o Scheduler avaliar uma Subtask `READY`, THE Agent_Registry SHALL retornar somente Specialized_Agents compatíveis com a capacidade requerida, com estado operacional que permita novas atribuições e permitidos pela política da Task.
3. WHEN mais de um Specialized_Agent for elegível, THE Scheduler SHALL aplicar a Agent_Selection_Policy configurada.
4. WHEN a mesma Agent_Selection_Policy avaliar o mesmo conjunto ordenável de candidatos e o mesmo estado operacional, THE Scheduler SHALL selecionar o mesmo Specialized_Agent.
5. IF a Agent_Selection_Policy estiver ausente, desconhecida ou inválida, THEN THE AI_Agent_Orchestrator SHALL indicar Readiness indisponível com o código `AGENT_SELECTION_POLICY_INVALID`.
6. IF nenhum Specialized_Agent for elegível, THEN THE Orchestrator SHALL marcar a Subtask como `FAILED` com o código `NO_ELIGIBLE_AGENT`.
7. WHEN um Specialized_Agent for selecionado para um Attempt, THE Scheduler SHALL persistir um Agent_Assignment com Assignment_ID antes do despacho.
8. WHEN um Agent_Assignment for despachado, THE Scheduler SHALL encaminhar o Attempt a exatamente um Specialized_Agent.
9. WHEN o mesmo Assignment_ID for processado novamente, THE Scheduler SHALL retornar o Agent_Assignment existente sem criar outro vínculo lógico.
10. WHEN um Principal com Scope `agents:read` solicitar `GET /api/v1/agents`, THE API_Service SHALL retornar identificador, tipo, capacidades, versão e estado operacional dos Specialized_Agents visíveis ao Principal.
11. IF um Principal autenticado sem Scope `agents:read` solicitar `GET /api/v1/agents`, THEN THE API_Service SHALL responder com HTTP `403` e um Error_Envelope.
12. IF a resposta de agentes contiver configuração interna, credencial, segredo ou agente não visível ao Principal, THEN THE API_Service SHALL remover o dado não autorizado antes de responder.

### Requirement 5: Ferramentas reais de logs

**User Story:** Como Log_Agent, quero pesquisar e agregar logs autorizados, para produzir evidências operacionais rastreáveis.

#### Acceptance Criteria

1. WHEN o Log_Agent propuser uma busca, THE Tool_Gateway SHALL exigir fonte lógica, operação, filtro, janela temporal e parâmetros de projeção e limitação definidos no JSON_Schema da Tool.
2. IF um parâmetro obrigatório de logs estiver ausente, inválido ou fora dos Reference_Limits, THEN THE Tool_Gateway SHALL rejeitar a Tool_Call com o código `TOOL_ARGUMENT_INVALID` sem acessar a fonte.
3. WHEN os parâmetros de logs forem válidos, THE Tool_Gateway SHALL normalizar fonte, índices, campos, filtro e janela temporal antes da avaliação da Tool_Policy.
4. WHEN uma busca canônica de logs for autorizada, THE Tool_Gateway SHALL executar uma Real_Tool contra a fonte de logs configurada.
5. THE Tool_Policy de logs SHALL limitar fontes, índices, campos, intervalo temporal, quantidade de registros, bytes e duração da consulta por Reference_Limits.
6. IF uma busca de logs referenciar fonte, índice ou campo fora da Allowlist, THEN THE Tool_Gateway SHALL rejeitar a Tool_Call com o código `TOOL_POLICY_DENIED`.
7. WHEN uma busca de logs alcançar o limite de registros ou bytes, THE Real_Tool SHALL retornar uma saída truncada com indicação explícita do limite alcançado.
8. WHEN uma busca válida não encontrar registros, THE Log_Agent SHALL retornar um Normalized_Result bem-sucedido com contagem zero e sem fabricar Source_References de evidência inexistente.
9. WHEN uma busca ou agregação de logs for concluída, THE Log_Agent SHALL incluir Source_References da fonte, janela temporal, representação do filtro redigida conforme a Tool_Policy, Tool_Call_ID, contagem e estado de truncamento no Normalized_Result.
10. IF a fonte de logs estiver indisponível e a Retry_Policy permitir outro Attempt, THEN THE Log_Agent SHALL classificar a falha como Transient_Failure e aplicar a Retry_Policy.
11. IF a fonte de logs permanecer indisponível após a Retry_Policy, THEN THE Log_Agent SHALL retornar um erro classificado sem fabricar evidências.

### Requirement 6: Ferramentas reais de banco de dados somente leitura

**User Story:** Como Database_Agent, quero inspecionar metadados e executar consultas estritamente somente leitura, para investigar dados sem alterar os dados de origem.

#### Acceptance Criteria

1. WHEN o Database_Agent propuser uma operação, THE Tool_Gateway SHALL exigir fonte lógica, tipo de operação e argumentos específicos definidos no JSON_Schema da Tool.
2. IF um parâmetro obrigatório de banco estiver ausente, inválido ou fora dos Reference_Limits, THEN THE Tool_Gateway SHALL rejeitar a Tool_Call com o código `TOOL_ARGUMENT_INVALID` sem acessar o banco.
3. WHEN identificadores de fonte, schema ou objeto forem recebidos, THE Tool_Gateway SHALL converter os identificadores para uma representação canônica antes da avaliação da Tool_Policy.
4. WHEN o Database_Agent solicitar inspeção de schema autorizada, THE Tool_Gateway SHALL executar uma Real_Tool usando uma identidade de banco configurada como somente leitura.
5. WHEN o Database_Agent submeter uma consulta, THE SQL_Query_Validator SHALL aceitar somente uma instrução do subconjunto configurado de `SELECT`, `WITH ... SELECT` ou `EXPLAIN` da gramática da versão suportada do PostgreSQL.
6. IF uma consulta contiver mais de uma instrução, comando de escrita, DDL, controle transacional, `COPY`, função não permitida ou acesso a objeto fora da Allowlist, THEN THE SQL_Query_Validator SHALL rejeitar a consulta com o código `READ_ONLY_QUERY_REQUIRED`.
7. THE SQL_Query_Printer SHALL serializar uma Read_Only_Query aceita em SQL canônico válido para a versão configurada do PostgreSQL.
8. WHEN uma Read_Only_Query for validada, impressa e validada novamente, THE SQL_Query_Validator SHALL produzir ASTs semanticamente equivalentes nas duas validações.
9. WHEN uma Read_Only_Query for executada, THE Real_Tool SHALL aplicar transação somente leitura, `statement_timeout`, limite de linhas e limite de bytes definidos nos Reference_Limits.
10. IF o limite de linhas ou bytes for alcançado, THEN THE Real_Tool SHALL retornar resultado truncado com indicação explícita do limite alcançado.
11. WHEN uma Read_Only_Query válida retornar zero linhas, THE Database_Agent SHALL produzir um Normalized_Result bem-sucedido com contagem zero.
12. WHEN uma consulta ou inspeção for concluída, THE Database_Agent SHALL incluir fonte lógica, objetos acessados, representação da operação redigida conforme a Tool_Policy, Tool_Call_ID, contagem e estado de truncamento no Normalized_Result.
13. IF a fonte de banco estiver indisponível e a Retry_Policy permitir outro Attempt, THEN THE Database_Agent SHALL classificar a falha como Transient_Failure e aplicar a Retry_Policy.
14. IF a fonte de banco permanecer indisponível após a Retry_Policy, THEN THE Database_Agent SHALL retornar um erro classificado sem fabricar dados.
15. IF a identidade de banco permitir escrita durante a verificação de inicialização, THEN THE Database_Agent SHALL permanecer indisponível com o código `DATABASE_ROLE_UNSAFE`.

### Requirement 7: Ferramentas reais de análise de código

**User Story:** Como Code_Analysis_Agent, quero buscar e ler código e dependências dentro de raízes autorizadas, para produzir análises reproduzíveis sem executar o projeto.

#### Acceptance Criteria

1. WHEN o Code_Analysis_Agent propuser uma operação, THE Tool_Gateway SHALL exigir raiz lógica, tipo de operação e os argumentos requeridos pelo tipo de operação conforme o JSON_Schema da Tool.
2. IF um parâmetro obrigatório de código estiver ausente, inválido ou fora dos Reference_Limits, THEN THE Tool_Gateway SHALL rejeitar a Tool_Call com o código `TOOL_ARGUMENT_INVALID` sem acessar o sistema de arquivos.
3. WHEN um caminho for recebido, THE Real_Tool SHALL resolver caminho canônico e links simbólicos antes da avaliação da Tool_Policy e antes de cada acesso.
4. IF um caminho canônico ou link simbólico sair das raízes da Allowlist, THEN THE Tool_Gateway SHALL rejeitar a Tool_Call com o código `PATH_OUTSIDE_ALLOWLIST`.
5. WHEN uma busca ou leitura canônica for autorizada, THE Tool_Gateway SHALL executar uma Real_Tool dentro da raiz de repositório configurada.
6. THE Tool_Policy de código SHALL limitar raízes, extensões, operações, quantidade de resultados, tamanho por arquivo, total de bytes e duração por Reference_Limits.
7. WHEN a inspeção de dependências for solicitada, THE Real_Tool SHALL ler somente manifestos e arquivos de lock presentes na Allowlist.
8. IF uma Tool_Call solicitar execução de código, build, script ou shell, THEN THE Tool_Gateway SHALL rejeitar a Tool_Call com o código `CODE_EXECUTION_DISABLED`.
9. WHEN uma busca válida não encontrar correspondências, THE Code_Analysis_Agent SHALL retornar um Normalized_Result bem-sucedido com contagem zero.
10. WHEN uma busca atingir o limite de resultados ou bytes, THE Real_Tool SHALL retornar saída truncada com indicação explícita do limite alcançado.
11. WHEN uma análise de código usar um arquivo como evidência, THE Code_Analysis_Agent SHALL incluir caminho relativo canônico, intervalo de linhas, hash do conteúdo, Tool_Call_ID, contagem e estado de truncamento na Source_Reference do arquivo.
12. IF um arquivo exceder o limite configurado ou for binário não permitido, THEN THE Real_Tool SHALL retornar somente os campos de metadados permitidos pela Tool_Policy sem carregar o conteúdo completo.
13. IF a raiz configurada estiver indisponível e a Retry_Policy permitir outro Attempt, THEN THE Code_Analysis_Agent SHALL classificar a falha como Transient_Failure e aplicar a Retry_Policy.
14. IF a raiz configurada permanecer indisponível após a Retry_Policy, THEN THE Code_Analysis_Agent SHALL retornar um erro classificado sem fabricar conteúdo.

### Requirement 8: Ferramentas reais de infraestrutura

**User Story:** Como Infrastructure_Agent, quero consultar saúde e métricas de alvos autorizados, para diagnosticar condições operacionais sem alterar recursos.

#### Acceptance Criteria

1. WHEN o Infrastructure_Agent propuser uma consulta, THE Tool_Gateway SHALL exigir alvo lógico, tipo de operação e os argumentos requeridos pelo tipo de operação conforme o JSON_Schema da Tool.
2. IF um parâmetro obrigatório de infraestrutura estiver ausente, inválido ou fora dos Reference_Limits, THEN THE Tool_Gateway SHALL rejeitar a Tool_Call com o código `TOOL_ARGUMENT_INVALID` sem acessar o alvo.
3. WHEN um destino for recebido ou redirecionado, THE Tool_Gateway SHALL resolver esquema, host, porta, endereço e caminho canônicos antes da avaliação da Tool_Policy e antes de cada conexão.
4. IF um destino resolvido ou redirecionado estiver fora da Allowlist, THEN THE Tool_Gateway SHALL rejeitar a Tool_Call com o código `TARGET_OUTSIDE_ALLOWLIST`.
5. WHEN uma consulta canônica de saúde ou métricas for autorizada, THE Tool_Gateway SHALL executar uma Real_Tool contra o alvo configurado.
6. THE Tool_Policy de infraestrutura SHALL limitar esquemas de URL, hosts, portas, endereços, caminhos, nomes de métricas, intervalos temporais, bytes e duração por Reference_Limits.
7. IF uma Tool_Call solicitar mutação, comando remoto ou alteração de configuração, THEN THE Tool_Gateway SHALL rejeitar a Tool_Call com o código `INFRASTRUCTURE_MUTATION_DISABLED`.
8. WHEN uma consulta válida não retornar amostras, THE Infrastructure_Agent SHALL produzir um Normalized_Result bem-sucedido com contagem zero.
9. WHEN um alvo responder com conteúdo acima do limite configurado, THE Real_Tool SHALL retornar uma saída truncada com indicação explícita do limite alcançado.
10. WHEN uma consulta de infraestrutura for concluída, THE Infrastructure_Agent SHALL incluir alvo lógico, destino canônico redigido, intervalo temporal, Tool_Call_ID, contagem e estado de truncamento no Normalized_Result.
11. IF um alvo estiver indisponível e a Retry_Policy permitir outro Attempt, THEN THE Infrastructure_Agent SHALL classificar a falha como Transient_Failure e aplicar a Retry_Policy.
12. IF um alvo permanecer indisponível após a Retry_Policy, THEN THE Infrastructure_Agent SHALL retornar um erro classificado sem fabricar métricas ou saúde.

### Requirement 9: Autorização e contenção de ferramentas

**User Story:** Como responsável de segurança, quero controlar cada Tool_Call fora do modelo de linguagem, para impedir que conteúdo não confiável amplie permissões.

#### Acceptance Criteria

1. WHEN um Specialized_Agent propuser uma Tool_Call, THE Tool_Gateway SHALL validar Tool_Call_ID, Tool, tipos, campos obrigatórios, tamanhos e argumentos contra o JSON_Schema versionado antes da autorização.
2. IF a validação da Tool_Call falhar, THEN THE Tool_Gateway SHALL rejeitar a proposta sem avaliar conteúdo não validado como permissão e sem executar a Tool.
3. WHEN os argumentos forem válidos, THE Tool_Gateway SHALL criar a Canonical_Tool_Request antes da Authorization_Decision.
4. WHEN a Canonical_Tool_Request estiver pronta, THE Authorization_Service SHALL avaliar Scopes do Principal, permissões do Specialized_Agent, Tool_Policy, alvo canônico e limites como uma única Authorization_Decision.
5. IF qualquer condição obrigatória da Authorization_Decision não conceder a operação completa, THEN THE Tool_Gateway SHALL rejeitar a Tool_Call com um código de política estável.
6. WHEN uma Authorization_Decision for produzida, THE Tool_Gateway SHALL persistir um registro de auditoria com decisão, versão da política, identidades, alvos redigidos e Trace_ID.
7. IF a Authorization_Decision e o registro de auditoria não puderem ser confirmados consistentemente, THEN THE Tool_Gateway SHALL rejeitar a Tool_Call com o código `AUDIT_UNAVAILABLE` sem executar a Tool.
8. WHEN uma Tool_Call for autorizada, THE Tool_Gateway SHALL reservar os limites definidos pela Tool_Policy e pelos Reference_Limits antes de iniciar a execução.
9. WHILE uma Tool_Call estiver em execução, THE Tool_Gateway SHALL aplicar timeout, limite de saída e limites de recursos definidos nos Reference_Limits.
10. WHERE uma Tool acessar arquivos ou processos locais, THE Tool_Gateway SHALL aplicar o isolamento definido pela Tool_Policy antes de permitir o acesso.
11. IF o isolamento obrigatório não puder ser aplicado, THEN THE Tool_Gateway SHALL rejeitar a Tool_Call com o código `TOOL_ISOLATION_UNAVAILABLE`.
12. WHEN uma Tool_Call for executada, THE Tool_Gateway SHALL associar Tool_Call_ID, Task_ID, Subtask_ID, Principal, Specialized_Agent e Trace_ID ao registro de auditoria.
13. WHEN uma Tool_Call com o mesmo Tool_Call_ID e a mesma Canonical_Tool_Request for repetida, THE Tool_Gateway SHALL retornar o resultado persistido ou o estado existente sem repetir o efeito externo.
14. IF um Tool_Call_ID for reutilizado com Canonical_Tool_Request diferente, THEN THE Tool_Gateway SHALL rejeitar a proposta com o código `TOOL_CALL_ID_CONFLICT`.
15. THE Tool_Gateway SHALL tratar Tool_Output como dados não confiáveis sem conceder capacidade de iniciar outra Tool_Call diretamente.
16. IF uma Task exceder o orçamento configurado de Tool_Calls, THEN THE Tool_Gateway SHALL rejeitar novas Tool_Calls com o código `TOOL_BUDGET_EXHAUSTED`.
17. WHEN argumentos ou resultados forem auditados, THE Tool_Gateway SHALL aplicar as regras configuradas de redução, hash ou redação de dados sensíveis.
18. IF um limite reservado não puder ser contabilizado consistentemente, THEN THE Tool_Gateway SHALL rejeitar a Tool_Call sem executar a Tool.

### Requirement 10: Concorrência, dependências e worker pools

**User Story:** Como Owner, quero que trabalho independente execute concorrentemente dentro de limites, para reduzir a duração sem comprometer estabilidade.

#### Acceptance Criteria

1. WHEN duas ou mais Subtasks estiverem `READY` e não houver caminho de Dependency entre as Subtasks, THE Scheduler SHALL permitir execução concorrente até os limites global e por Task configurados.
2. WHILE uma Dependency obrigatória não estiver `SUCCEEDED`, THE Scheduler SHALL manter a Subtask sucessora em `BLOCKED`.
3. WHEN todas as Dependencies obrigatórias de uma Subtask forem `SUCCEEDED`, THE Scheduler SHALL mover a Subtask de `BLOCKED` para `READY` por uma transição confirmada.
4. WHEN uma Subtask `READY` for escolhida, THE Scheduler SHALL reservar atomicamente os slots global e por Task antes de mover a Subtask para `RUNNING`.
5. IF um slot necessário não estiver disponível, THEN THE Scheduler SHALL manter a Subtask elegível sem criar execução excedente.
6. THE AI_Agent_Orchestrator SHALL executar trabalho concorrente por goroutines controladas por Worker_Pools e Bounded_Queues.
7. THE AI_Agent_Orchestrator SHALL propagar Execution_Context por planejamento, despacho, Specialized_Agents, Tool_Gateway, LLM_Gateway e Aggregator.
8. WHILE uma Task consumir o limite por Task, THE Scheduler SHALL reservar a capacidade global restante para outras Tasks elegíveis.
9. WHEN múltiplas Tasks disputarem capacidade, THE Scheduler SHALL aplicar a política configurada de fairness antes de conceder slots adicionais a uma Task já em execução.
10. THE Scheduler SHALL limitar filas internas e trabalho em voo pelas capacidades definidas nos Reference_Limits.
11. IF uma Bounded_Queue alcançar a capacidade configurada, THEN THE Scheduler SHALL interromper novos envios para a fila até existir capacidade ou o Execution_Context terminar.
12. IF a capacidade global de admissão de novas Tasks estiver esgotada, THEN THE API_Service SHALL responder a novas submissões com HTTP `503`, Error_Envelope e `Retry-After`.
13. WHEN um Worker adquirir um slot, THE Worker_Pool SHALL associar a aquisição à unidade de trabalho correspondente.
14. WHEN um Worker concluir, falhar, cancelar ou abandonar trabalho, THE Worker_Pool SHALL liberar cada slot adquirido exatamente uma vez.
15. IF o Execution_Context terminar enquanto uma goroutine aguardar channel, Bounded_Queue, slot, Lease, Tool ou resposta externa, THEN THE Worker_Pool SHALL desbloquear a goroutine pelo caminho de cancelamento.
16. IF uma Subtask for cancelada antes de iniciar, THEN THE Scheduler SHALL remover ou ignorar de modo idempotente a entrada pendente sem consumir slot de execução.

### Requirement 11: Retries, timeouts e idempotência

**User Story:** Como operador, quero recuperação limitada de falhas transitórias, para aumentar a conclusão sem gerar tempestades de repetição ou efeitos duplicados.

#### Acceptance Criteria

1. THE AI_Agent_Orchestrator SHALL atribuir timeout finito a Task, Subtask, chamada ao Provider_LLM e Tool_Call conforme os Reference_Limits.
2. WHEN uma operação falhar, THE AI_Agent_Orchestrator SHALL classificar a falha como Transient_Failure ou Permanent_Failure antes de decidir por outro Attempt.
3. WHEN ocorrer uma Transient_Failure em operação idempotente e restarem Attempts, THE AI_Agent_Orchestrator SHALL aplicar a Retry_Policy configurada.
4. IF uma operação não possuir garantia idempotente verificável, THEN THE AI_Agent_Orchestrator SHALL encerrar a operação sem retry automático.
5. IF ocorrer uma Permanent_Failure, THEN THE AI_Agent_Orchestrator SHALL encerrar a operação afetada sem consumir outro Attempt.
6. WHEN uma resposta remota fornecer `Retry-After`, THE AI_Agent_Orchestrator SHALL respeitar o valor sem ultrapassar o prazo restante da operação.
7. WHEN um Attempt iniciar, THE PostgreSQL_Store SHALL persistir Attempt_ID, número, prazo, operação, identificador idempotente e classificação pendente antes de iniciar o processamento do Attempt.
8. WHEN um Attempt terminar, THE PostgreSQL_Store SHALL persistir duração, resultado e classificação da falha disponível.
9. WHEN o mesmo Attempt_ID e a mesma operação forem recebidos novamente, THE AI_Agent_Orchestrator SHALL retornar o estado persistido sem criar outro Attempt lógico.
10. IF um Attempt_ID for reutilizado para operação diferente, THEN THE AI_Agent_Orchestrator SHALL rejeitar a operação com o código `ATTEMPT_ID_CONFLICT`.
11. IF o timeout de uma operação expirar, THEN THE AI_Agent_Orchestrator SHALL cancelar o Execution_Context da operação e classificar o resultado como `TIMEOUT`.
12. IF a quantidade máxima de Attempts for alcançada, THEN THE AI_Agent_Orchestrator SHALL encerrar a operação com o código de esgotamento correspondente e persistir um Task_Event.
13. IF o prazo total da Task expirar, THEN THE Orchestrator SHALL impedir novos Attempts e solicitar o fechamento do DAG com as causas correspondentes.
14. WHEN uma Delivery_Attempt for repetida, THE Asynchronous_Dispatch SHALL preservar o identificador idempotente do trabalho.
15. IF a quantidade máxima de Delivery_Attempts for excedida, THEN THE Asynchronous_Dispatch SHALL criar um Dead_Letter_Record rastreável.

### Requirement 12: Cancelamento cooperativo

**User Story:** Como Owner, quero cancelar uma Task em execução, para interromper consumo adicional de recursos de forma rastreável.

#### Acceptance Criteria

1. WHEN o Owner autorizado enviar `POST /api/v1/tasks/{id}/cancel` para uma Task não terminal, THE API_Service SHALL registrar a intenção de cancelamento e o Task_Event correspondente como uma única mudança consistente.
2. WHEN a mesma intenção de cancelamento for repetida, THE API_Service SHALL retornar o estado já registrado sem criar outro Task_Event de intenção.
3. WHEN a intenção de cancelamento for registrada, THE Orchestrator SHALL sinalizar o Execution_Context raiz em até 1 segundo.
4. WHEN o Execution_Context raiz for sinalizado, THE AI_Agent_Orchestrator SHALL propagar o cancelamento aos Execution_Contexts de planejamento, despacho, Subtasks, Tool_Calls e agregação.
5. WHILE uma Task tiver cancelamento registrado, THE Scheduler SHALL impedir o início de novas Subtasks e Tool_Calls.
6. WHEN uma operação cooperativa receber o sinal de cancelamento, THE AI_Agent_Orchestrator SHALL encerrar a operação dentro do menor valor entre o timeout restante e o Grace_Period de cancelamento.
7. WHEN cancelamento e conclusão disputarem a mesma Task, THE PostgreSQL_Store SHALL confirmar no máximo uma transição terminal a partir da versão vigente.
8. IF uma transição terminal diferente de `CANCELLED` for confirmada antes da intenção de cancelamento, THEN THE API_Service SHALL responder ao cancelamento com HTTP `409` e um Error_Envelope.
9. IF a intenção de cancelamento for confirmada antes de um resultado concorrente, THEN THE Orchestrator SHALL impedir que o resultado concorrente substitua o estado `CANCELLED`.
10. WHEN um Late_Result chegar após a confirmação de `CANCELLED`, THE Orchestrator SHALL registrar metadados auditáveis do Late_Result sem incorporar o Late_Result ao Final_Result.
11. WHEN as operações ativas encerrarem ou o Grace_Period expirar, THE Orchestrator SHALL mover a Task para `CANCELLED` e persistir o Task_Event terminal.
12. WHEN o endpoint de cancelamento receber repetição para uma Task `CANCELLED`, THE API_Service SHALL responder com HTTP `200` e o estado terminal existente.
13. IF o endpoint de cancelamento referenciar uma Task terminal diferente de `CANCELLED`, THEN THE API_Service SHALL responder com HTTP `409` e um Error_Envelope.
14. IF o Principal não puder cancelar a Task, THEN THE API_Service SHALL responder com HTTP `404` sem revelar a existência da Task.

### Requirement 13: Tolerância a falhas parciais

**User Story:** Como Owner, quero receber resultados úteis mesmo quando parte do plano falhar, para aproveitar evidências independentes e entender as lacunas.

#### Acceptance Criteria

1. IF uma Subtask falhar, THEN THE Scheduler SHALL continuar Subtasks independentes que permaneçam elegíveis.
2. IF uma Dependency obrigatória terminar em `FAILED` ou `CANCELLED`, THEN THE Scheduler SHALL marcar a Subtask dependente como `SKIPPED` com a causa correspondente.
3. WHEN uma Subtask for marcada como `SKIPPED`, THE Scheduler SHALL reavaliar transitivamente as Subtasks dependentes até não restar sucessora bloqueada pela mesma falha.
4. IF prazo, orçamento ou cancelamento impedir trabalho ainda não iniciado, THEN THE Scheduler SHALL mover cada Subtask afetada para `SKIPPED` ou `CANCELLED` com causa classificada.
5. WHEN todas as Subtasks alcançarem estado terminal, THE Orchestrator SHALL confirmar a DAG_Closure antes de iniciar a agregação.
6. WHILE a DAG_Closure não estiver confirmada, THE Orchestrator SHALL manter a Task fora de `AGGREGATING`.
7. WHEN todas as Subtasks requeridas terminarem com `SUCCEEDED` e existir Useful_Result, THE Orchestrator SHALL mover a Task para `AGGREGATING` e permitir resultado `COMPLETED`.
8. WHEN existir pelo menos um Useful_Result e pelo menos uma Subtask `FAILED` ou `SKIPPED`, THE Orchestrator SHALL mover a Task para `AGGREGATING` e permitir resultado `PARTIALLY_COMPLETED`.
9. IF o planejamento falhar ou nenhuma Subtask produzir Useful_Result, THEN THE Orchestrator SHALL mover a Task para `FAILED` com causas classificadas.
10. WHEN uma Task for agregada com falhas parciais, THE Aggregator SHALL incluir Subtasks ausentes, causas, impacto e evidências disponíveis no Final_Result.
11. IF o PostgreSQL_Store permanecer disponível durante falha de um Specialized_Agent, THEN THE Orchestrator SHALL preservar os resultados confirmados dos outros Specialized_Agents.

### Requirement 14: Agregação fundamentada por LLM

**User Story:** Como Owner, quero uma síntese final rastreável, para distinguir fatos observados, inferências e lacunas.

#### Acceptance Criteria

1. WHEN uma Task entrar em `AGGREGATING`, THE Aggregator SHALL fornecer ao LLM_Gateway somente os campos de Normalized_Results permitidos pela política de dados e pelo JSON_Schema de agregação.
2. THE Aggregator SHALL validar a saída de agregação contra o JSON_Schema versionado do Final_Result.
3. THE Aggregator SHALL incluir no Final_Result resumo, conclusões, Source_References, falhas parciais, limitações e próximos passos sugeridos.
4. WHEN o Provider_LLM produzir uma afirmação factual, THE LLM_Response_Parser SHALL exigir pelo menos uma Source_Reference válida.
5. WHEN o Provider_LLM produzir uma inferência sem evidência direta, THE LLM_Response_Parser SHALL exigir marcação explícita de inferência e das limitações correspondentes.
6. IF o Provider_LLM referenciar Source_Reference inexistente ou incompatível com a afirmação, THEN THE Aggregator SHALL rejeitar a resposta e solicitar correção dentro da Retry_Policy.
7. IF a agregação permanecer inválida após a Retry_Policy, THEN THE Aggregator SHALL produzir um Final_Result determinístico com os Normalized_Results e o erro `AGGREGATION_INVALID`.
8. IF o Provider_LLM estiver indisponível após a Retry_Policy e existir Useful_Result, THEN THE Aggregator SHALL produzir um Final_Result determinístico sem LLM.
9. WHEN o Aggregator produzir um fallback determinístico, THE Aggregator SHALL ordenar resultados por identificadores estáveis e preservar todas as Source_References válidas.
10. WHEN os mesmos Normalized_Results forem fornecidos em ordens diferentes ao fallback determinístico, THE Aggregator SHALL produzir Final_Results semanticamente equivalentes.
11. WHEN o Final_Result incluir conteúdo gerado pelo modelo, THE Aggregator SHALL identificar o conteúdo separadamente de fatos extraídos por Tools.
12. WHEN o Final_Result estiver validado, THE PostgreSQL_Store SHALL persistir o resultado antes da transição terminal correspondente.
13. WHEN a agregação terminar com todos os resultados requeridos, THE Orchestrator SHALL mover a Task para `COMPLETED`.
14. WHEN a agregação terminar com Useful_Results e falhas parciais, THE Orchestrator SHALL mover a Task para `PARTIALLY_COMPLETED`.

### Requirement 15: Progresso em tempo real e retomada

**User Story:** Como Owner, quero acompanhar eventos em tempo real e retomar após desconexão, para observar execuções longas sem polling contínuo.

#### Acceptance Criteria

1. WHEN um Principal autorizado abrir `GET /api/v1/tasks/{id}/events`, THE API_Service SHALL iniciar um Event_Stream da Task.
2. WHEN um cliente autorizado abrir o Event_Stream sem Resume_Cursor, THE Event_Stream SHALL retransmitir os Task_Events retidos desde o Event_ID mais antigo antes de enviar eventos novos.
3. WHEN um cliente fornecer um Resume_Cursor válido, THE Event_Stream SHALL retransmitir os Task_Events posteriores ao cursor na ordem persistida antes de enviar eventos novos.
4. IF o Resume_Cursor estiver malformado, pertencer a outra Task ou for posterior ao Event_ID mais recente confirmado, THEN THE API_Service SHALL responder com HTTP `400` e um Error_Envelope com o código `EVENT_CURSOR_INVALID`.
5. IF o Resume_Cursor for anterior ao Task_Event mais antigo retido, THEN THE API_Service SHALL responder com HTTP `410` e um Error_Envelope com o código `EVENT_CURSOR_EXPIRED`.
6. WHEN uma mudança de estado ou progresso for confirmada, THE PostgreSQL_Store SHALL persistir um Task_Event com Event_ID, tipo, horário UTC e payload versionado.
7. WHEN o Event_Stream passar de replay para entrega ao vivo, THE Event_Stream SHALL preservar a sequência confirmada sem omitir Task_Events.
8. WHILE o sistema operar dentro dos Reference_Limits, THE Event_Stream SHALL atender à definição de Real_Time.
9. THE Event_Stream SHALL ordenar Task_Events de uma Task por Event_ID monotônico.
10. WHEN um Task_Event for retransmitido, THE Event_Stream SHALL preservar o mesmo Event_ID para permitir deduplicação pelo cliente.
11. WHILE não houver Task_Event por 15 segundos, THE Event_Stream SHALL emitir um heartbeat sem criar Event_ID e sem alterar o histórico da Task.
12. IF o buffer de um cliente atingir a capacidade configurada por consumo lento, THEN THE Event_Stream SHALL emitir um evento de controle com o último Resume_Cursor enfileirado.
13. WHEN o evento de controle de Backpressure for emitido, THE Event_Stream SHALL encerrar a conexão em até 1 segundo sem bloquear Worker_Pools.
14. WHEN uma Task alcançar Terminal_Task_Status, THE Event_Stream SHALL emitir o Task_Event terminal antes do encerramento normal.
15. IF a Authorization_Credential do Event_Stream expirar, THEN THE API_Service SHALL encerrar o stream em até 30 segundos após a expiração.
16. WHEN um cliente desconectar o Event_Stream, THE API_Service SHALL liberar os recursos da conexão sem cancelar a Task.
17. IF o Task_ID não existir ou não estiver visível ao Principal, THEN THE API_Service SHALL responder ao pedido de Event_Stream com HTTP `404` sem revelar a causa.

### Requirement 16: Persistência durável e recuperação

**User Story:** Como operador, quero recuperar trabalho aceito após reinício, para evitar perda silenciosa de Tasks e resultados.

#### Acceptance Criteria

1. THE PostgreSQL_Store SHALL ser a fonte durável de verdade para Tasks, Execution_Plans, Subtasks, Attempts, Tool_Calls, Agent_Assignments, Task_Events, Dead_Letter_Records e Final_Results.
2. WHEN uma Task for aceita, THE PostgreSQL_Store SHALL confirmar o Task_Record e a elegibilidade para despacho como uma única mudança consistente antes da resposta HTTP `202`.
3. THE Redis_Service SHALL armazenar somente estado reconstruível ou efêmero.
4. IF o Redis_Service perder dados, THEN THE AI_Agent_Orchestrator SHALL reconstruir o estado necessário a partir do PostgreSQL_Store sem perder Task_Records confirmados.
5. WHEN um processo adquirir trabalho recuperável, THE Asynchronous_Dispatch SHALL registrar um Lease com identidade do proprietário e expiração.
6. WHILE um Lease válido existir, THE Asynchronous_Dispatch SHALL impedir outro consumidor de adquirir a mesma unidade lógica de trabalho.
7. WHEN um processo iniciar, THE Orchestrator SHALL identificar Tasks não terminais e trabalho sem Lease válido para recuperação idempotente.
8. WHEN trabalho recuperável for redistribuído, THE Asynchronous_Dispatch SHALL preservar Task_ID, Subtask_ID, Attempt_ID e identificador idempotente.
9. WHEN um resultado recuperado já estiver confirmado, THE Orchestrator SHALL reutilizar o resultado sem repetir o efeito externo.
10. IF o Message_Backbone estiver temporariamente indisponível após a aceitação de uma Task, THEN THE PostgreSQL_Store SHALL manter o trabalho pendente elegível para despacho posterior.
11. IF o PostgreSQL_Store obrigatório estiver indisponível, THEN THE Readiness SHALL indicar indisponibilidade e a API_Service SHALL rejeitar novas Tasks com HTTP `503`.
12. WHEN a conectividade obrigatória for restaurada, THE Orchestrator SHALL retomar o despacho de trabalho persistido dentro dos Reference_Limits.
13. IF a recuperação de uma unidade de trabalho falhar e a Retry_Policy permitir outro Attempt, THEN THE Orchestrator SHALL manter a unidade recuperável e aplicar a Retry_Policy.
14. IF a recuperação de uma unidade de trabalho permanecer impossível após a Retry_Policy, THEN THE Asynchronous_Dispatch SHALL criar um Dead_Letter_Record com causa classificada e conteúdo redigido conforme a política de dados.
15. WHEN um Dead_Letter_Record de recuperação for confirmado, THE Orchestrator SHALL aplicar as regras de falha parcial à Task afetada sem descartar outros trabalhos recuperáveis.

### Requirement 17: Autenticação e autorização

**User Story:** Como responsável de segurança, quero controlar acesso por identidade, escopo e propriedade, para proteger Tasks, agentes e ferramentas.

#### Acceptance Criteria

1. WHEN uma Authorization_Credential for apresentada, THE Authentication_Service SHALL validar assinatura, emissor, audiência, validade temporal e algoritmo permitido.
2. IF uma rota protegida não receber Authorization_Credential válida, THEN THE API_Service SHALL responder com HTTP `401` e um Error_Envelope antes de consultar a existência do recurso.
3. WHEN uma Task for criada, THE API_Service SHALL definir o Owner a partir do Principal autenticado.
4. IF o payload de criação tentar definir ou substituir o Owner, THEN THE API_Service SHALL rejeitar a submissão com HTTP `400` e um Error_Envelope.
5. THE Authorization_Service SHALL exigir Scope `tasks:create` para criar Task, `tasks:read` para consultar Task ou eventos, `tasks:cancel` para cancelar Task e `agents:read` para consultar agentes.
6. IF um Principal autenticado não possuir o Scope exigido por uma operação sem ocultação por propriedade, THEN THE API_Service SHALL responder com HTTP `403` antes de executar a operação.
7. WHEN um Principal com Scope suficiente acessar uma Task, THE Authorization_Service SHALL exigir propriedade da Task ou Role administrativa explicitamente autorizada.
8. IF um Principal com Scope suficiente tentar acessar Task de outro Owner sem Role administrativa autorizada, THEN THE API_Service SHALL responder com HTTP `404`.
9. IF um Task_ID consultado por Principal autorizado não existir, THEN THE API_Service SHALL responder com o mesmo formato HTTP `404` usado para falta de propriedade.
10. WHEN uma Role administrativa autorizar acesso a Task de outro Owner, THE Authorization_Service SHALL exigir também o Scope específico da operação.
11. WHEN uma Role administrativa for usada, THE Authorization_Service SHALL registrar Principal, Role, operação, Task_ID e Trace_ID em auditoria.
12. WHEN uma Tool_Call for avaliada, THE Authorization_Service SHALL usar a identidade e os Scopes originais da Task.
13. WHERE Local_Development_Mode estiver habilitado, THE Authentication_Service SHALL aceitar somente credenciais de desenvolvimento explicitamente configuradas e identificadas em auditoria.
14. IF credenciais de desenvolvimento forem configuradas fora de Local_Development_Mode, THEN THE AI_Agent_Orchestrator SHALL falhar a validação de inicialização.
15. IF uma credencial local tentar autorizar fonte, Tool ou ambiente fora da configuração de desenvolvimento, THEN THE Authorization_Service SHALL negar a operação com um código de política estável.

### Requirement 18: Rate limiting, cotas e orçamento

**User Story:** Como operador, quero limitar uso e custo por identidade, para manter disponibilidade e controlar consumo externo.

#### Acceptance Criteria

1. THE Rate_Limiter SHALL aplicar limites configuráveis por Principal e origem às operações de criação, consulta, cancelamento e Event_Stream.
2. WHEN solicitações concorrentes consumirem a mesma cota, THE Rate_Limiter SHALL contabilizar cada operação aceita atomicamente.
3. WHEN uma repetição idempotente reutilizar o mesmo identificador da operação, THE Rate_Limiter SHALL evitar contabilização duplicada da mesma aceitação lógica.
4. IF um limite de API for excedido, THEN THE API_Service SHALL responder com HTTP `429`, Error_Envelope e `Retry-After` calculado pela política vigente.
5. THE Orchestrator SHALL aplicar por Task limites configurados de Subtasks, duração total, Attempts, Tool_Calls e tokens do Provider_LLM.
6. WHEN uma operação sujeita a orçamento iniciar, THE Orchestrator SHALL reservar atomicamente a unidade definida para o orçamento correspondente antes do consumo externo.
7. IF uma reserva de orçamento exceder o saldo disponível, THEN THE Orchestrator SHALL rejeitar a operação sem iniciar o consumo externo.
8. WHEN uma operação reservada terminar, THE Orchestrator SHALL reconciliar a reserva com o consumo confirmado sem contabilizar novamente uma repetição do mesmo identificador.
9. WHEN o consumo alcançar 80% de um orçamento de Task, THE Orchestrator SHALL persistir um Task_Event de aviso para o orçamento correspondente.
10. IF o orçamento de tokens do Provider_LLM for esgotado, THEN THE LLM_Gateway SHALL rejeitar novas chamadas com o código `LLM_BUDGET_EXHAUSTED`.
11. IF o orçamento de Tool_Calls for esgotado, THEN THE Tool_Gateway SHALL rejeitar novas Tool_Calls com o código `TOOL_BUDGET_EXHAUSTED`.
12. IF um orçamento impedir trabalho restante e existir Useful_Result, THEN THE Orchestrator SHALL solicitar DAG_Closure e agregação determinística para um resultado `PARTIALLY_COMPLETED`.
13. IF um orçamento impedir trabalho restante e não existir Useful_Result, THEN THE Orchestrator SHALL mover a Task para `FAILED` com o código de orçamento correspondente.
14. IF o Redis_Service usado pelo Rate_Limiter estiver indisponível, THEN THE API_Service SHALL responder a novas Tasks e novos Event_Streams com HTTP `503`, Error_Envelope e `Retry-After`.
15. IF o Redis_Service usado pelo Rate_Limiter estiver indisponível, THEN THE Rate_Limiter SHALL aplicar um limite emergencial finito e configurado por processo às consultas e aos cancelamentos autenticados.
16. WHEN solicitações concorrentes usarem o limite emergencial, THE Rate_Limiter SHALL contabilizar as aceitações atomicamente dentro do processo.
17. IF o Redis_Service usado pelo Rate_Limiter estiver indisponível, THEN THE API_Service SHALL reportar Readiness indisponível no endpoint de saúde.
18. WHEN o Redis_Service voltar a ficar disponível, THE Rate_Limiter SHALL retomar a política distribuída sem converter contabilizações idempotentes em consumo duplicado.

### Requirement 19: Segredos e proteção de dados

**User Story:** Como responsável de segurança, quero segredos e dados sensíveis minimizados, para reduzir exposição em execução, persistência e telemetria.

#### Acceptance Criteria

1. THE AI_Agent_Orchestrator SHALL obter credenciais do Provider_LLM, fontes de dados e serviços por Secret_Manager ou referência de segredo em tempo de execução.
2. IF um segredo obrigatório estiver ausente, inválido ou expirado, THEN THE AI_Agent_Orchestrator SHALL falhar a Readiness com um código de configuração estável que não inclua o segredo.
3. WHEN uma configuração, imagem ou arquivo de Docker Compose for produzido, THE AI_Agent_Orchestrator SHALL usar referências ou placeholders em lugar de segredos reais.
4. WHEN um segredo for usado, THE AI_Agent_Orchestrator SHALL impedir a persistência do valor em Task_Records, Task_Events, Structured_Logs e traces.
5. WHEN Structured_Logs, Task_Events ou traces forem emitidos, THE Observability_Service SHALL aplicar a política de redação configurada antes da exportação.
6. IF a política obrigatória de redação estiver ausente, inválida ou não puder ser aplicada, THEN THE Observability_Service SHALL bloquear a exportação do conteúdo afetado e emitir um código estável sem incluir o conteúdo afetado.
7. WHEN dados de Tool_Output forem preparados para o Provider_LLM, THE LLM_Gateway SHALL aplicar Allowlist de campos, redução de conteúdo e política de redação configuradas.
8. IF a Allowlist ou a política de redação exigida para envio externo estiver ausente ou inválida, THEN THE LLM_Gateway SHALL bloquear o envio do conteúdo afetado.
9. IF uma política proibir envio externo de uma fonte, THEN THE LLM_Gateway SHALL excluir o conteúdo da requisição e registrar a restrição sem incluir o conteúdo proibido.
10. WHERE uma conexão não local transportar Authorization_Credential, segredo ou Tool_Output, THE AI_Agent_Orchestrator SHALL exigir TLS validado.
11. IF a validação TLS de uma conexão protegida falhar, THEN THE AI_Agent_Orchestrator SHALL interromper a conexão sem enviar o conteúdo protegido.
12. WHEN um segredo novo e válido for disponibilizado na fonte configurada, THE AI_Agent_Orchestrator SHALL adotar o segredo novo dentro do intervalo configurado de recarga sem reconstruir a imagem.
13. IF a recarga fornecer um segredo inválido enquanto o segredo atual permanecer válido, THEN THE AI_Agent_Orchestrator SHALL manter o segredo atual e registrar a falha de modo redigido.
14. IF a recarga falhar e nenhum segredo válido permanecer disponível, THEN THE AI_Agent_Orchestrator SHALL rejeitar novas operações dependentes e indicar Readiness indisponível.

### Requirement 20: Defesa contra prompt injection

**User Story:** Como responsável de segurança, quero que instruções não confiáveis permaneçam dados, para impedir escalada de ferramentas e exfiltração induzida por prompt.

#### Acceptance Criteria

1. THE LLM_Gateway SHALL classificar Task_Description, Tool_Output, logs, dados de banco, código e métricas como conteúdo não confiável.
2. WHEN o LLM_Gateway construir uma requisição, THE LLM_Gateway SHALL separar instruções de sistema, políticas, dados não confiáveis e schemas em campos ou delimitadores inequívocos.
3. WHEN uma Structured_LLM_Response for recebida, THE LLM_Response_Parser SHALL interpretar somente campos permitidos pelo JSON_Schema como comandos estruturados.
4. WHEN o Provider_LLM sugerir uma Tool_Call, THE Tool_Gateway SHALL tratar a sugestão como proposta sujeita à validação e à Authorization_Decision externas ao Provider_LLM.
5. IF conteúdo não confiável solicitar segredo, mudança de Role, expansão de Scope ou Tool fora da Allowlist, THEN THE Tool_Gateway SHALL rejeitar a ação resultante com o código `PROMPT_INJECTION_POLICY_DENIED`.
6. THE Provider_LLM SHALL receber identificadores indiretos em lugar de credenciais utilizáveis para Tools.
7. WHEN Tool_Output retornar instruções dirigidas ao agente, THE Specialized_Agent SHALL preservar o conteúdo como evidência sem alterar Tool_Policy, Role, Scope ou Authorization_Credential.
8. IF uma regra configurada detectar padrão de prompt injection com ação `BLOCK`, THEN THE LLM_Gateway SHALL bloquear a operação afetada e registrar um evento de segurança redigido.
9. IF uma regra configurada detectar padrão de prompt injection com ação `FLAG`, THEN THE LLM_Gateway SHALL marcar o conteúdo e preservar todas as validações e autorizações subsequentes.
10. IF o mecanismo obrigatório de detecção estiver indisponível e a política exigir bloqueio, THEN THE LLM_Gateway SHALL bloquear o processamento do conteúdo afetado com um código estável sem incluir o conteúdo afetado.
11. WHEN o Final_Result incluir conteúdo não confiável, THE Aggregator SHALL identificar a origem e evitar apresentar o conteúdo como instrução operacional confiável.
12. IF a separação entre política e dados não confiáveis não puder ser construída conforme o contrato, THEN THE LLM_Gateway SHALL rejeitar a chamada sem enviar dados ao Provider_LLM.

### Requirement 21: Integração controlada com LLM

**User Story:** Como operador, quero trocar modelos compatíveis por configuração, para evitar acoplamento desnecessário a um único fornecedor.

#### Acceptance Criteria

1. THE LLM_Gateway SHALL suportar um Provider_LLM configurável compatível com os contratos necessários da API da OpenAI.
2. WHEN a configuração do Provider_LLM mudar, THE LLM_Gateway SHALL preservar os JSON_Schemas e a semântica pública de Task, planejamento e agregação.
3. WHEN o AI_Agent_Orchestrator validar a configuração, THE LLM_Gateway SHALL verificar as capacidades exigidas do Provider_LLM antes de indicar Readiness disponível.
4. THE LLM_Gateway SHALL usar tool/function calling e Structured_LLM_Response validada por JSON_Schema para planejamento e chamadas estruturadas.
5. WHEN uma chamada ao Provider_LLM iniciar, THE LLM_Gateway SHALL aplicar modelo, timeout, limite de tokens e identificador idempotente configurados.
6. WHEN o Provider_LLM retornar conteúdo estruturado, THE LLM_Response_Parser SHALL validar sintaxe, versão e JSON_Schema antes de disponibilizar a resposta ao chamador.
7. IF o Provider_LLM retornar Transient_Failure, THEN THE LLM_Gateway SHALL aplicar Retry_Policy à operação idempotente.
8. WHEN uma Transient_Failure incluir `Retry-After`, THE LLM_Gateway SHALL respeitar o valor sem ultrapassar o prazo restante da operação.
9. IF o Provider_LLM retornar erro de autenticação, schema incompatível ou requisição inválida, THEN THE LLM_Gateway SHALL classificar a falha como Permanent_Failure.
10. WHEN o Provider_LLM retornar erro específico do fornecedor, THE LLM_Gateway SHALL converter o erro específico para a classificação estável do AI_Agent_Orchestrator.
11. WHEN uma chamada ao Provider_LLM terminar, THE LLM_Gateway SHALL registrar métricas de duração, tokens informados, Attempts e código de resultado sem registrar segredo ou conteúdo sensível.
12. IF o Provider_LLM omitir metadados de consumo, THEN THE LLM_Gateway SHALL marcar o valor correspondente como desconhecido sem fabricar medição.
13. IF o Provider_LLM permanecer indisponível após a Retry_Policy durante planejamento, THEN THE Orchestrator SHALL mover a Task para `FAILED` com o código `LLM_UNAVAILABLE`.
14. IF o Provider_LLM permanecer indisponível após a Retry_Policy durante agregação e existir Useful_Result, THEN THE Aggregator SHALL produzir o Final_Result determinístico sem LLM.

### Requirement 22: Despacho assíncrono e semântica de mensageria

**User Story:** Como operador, quero despacho durável com semântica explícita, para escalar workers e recuperar entregas interrompidas.

#### Acceptance Criteria

1. THE Asynchronous_Dispatch SHALL oferecer entrega pelo menos uma vez com Acknowledgement explícito por consumidor.
2. WHEN um consumidor receber trabalho, THE Asynchronous_Dispatch SHALL fornecer Task_ID, Subtask_ID, Attempt_ID, prazo, identificador idempotente e Trace_Context.
3. WHEN um consumidor adquirir uma entrega, THE Asynchronous_Dispatch SHALL associar a Delivery_Attempt a um Lease vigente.
4. WHEN um consumidor enviar Acknowledgement, THE Asynchronous_Dispatch SHALL validar consumidor, Delivery_Attempt, identificador idempotente e Lease vigente.
5. IF um Acknowledgement usar Lease expirado, consumidor diferente ou identificador incompatível, THEN THE Asynchronous_Dispatch SHALL rejeitar o Acknowledgement sem confirmar a entrega.
6. WHEN o processamento produzir resultado, THE Asynchronous_Dispatch SHALL aceitar o Acknowledgement somente após a confirmação durável do resultado ou do estado terminal correspondente.
7. IF um consumidor perder o Lease ou a conexão antes do Acknowledgement válido, THEN THE Asynchronous_Dispatch SHALL tornar o trabalho elegível para nova Delivery_Attempt.
8. WHEN uma entrega duplicada encontrar resultado já confirmado, THE Asynchronous_Dispatch SHALL reutilizar o resultado sem repetir o efeito externo.
9. THE Asynchronous_Dispatch SHALL limitar backlog, consumidores concorrentes e trabalho em voo por Reference_Limits.
10. WHEN o backlog alcançar o limiar configurado, THE Asynchronous_Dispatch SHALL emitir Backpressure e métricas de saturação.
11. IF uma mensagem não puder ser processada após a política de entrega, THEN THE Asynchronous_Dispatch SHALL criar exatamente um Dead_Letter_Record para a unidade lógica com causa classificada e payload redigido conforme a política de dados.
12. WHEN múltiplos processos consumidores estiverem ativos, THE Asynchronous_Dispatch SHALL coordenar propriedade do trabalho sem depender de memória compartilhada do processo.
13. WHEN um processo reiniciar, THE Asynchronous_Dispatch SHALL recuperar entregas sem Acknowledgement válido e Leases expirados a partir do estado durável.
14. WHEN a fase de design selecionar o Message_Backbone, THE Decision_Record SHALL comparar Redis Streams, NATS com JetStream e RabbitMQ por todos os critérios definidos na seção “Decisões obrigatórias para a fase de design”.

### Requirement 23: Observabilidade completa

**User Story:** Como operador, quero correlacionar uma requisição com Tasks, Subtasks, Tools e dependências, para diagnosticar comportamento concorrente e distribuído.

#### Acceptance Criteria

1. THE Observability_Service SHALL emitir traces, métricas e Structured_Logs por OpenTelemetry.
2. WHEN uma requisição criar trabalho assíncrono, THE AI_Agent_Orchestrator SHALL vincular o Trace_Context da requisição ao trabalho persistido.
3. WHEN trabalho atravessar goroutine, channel, Bounded_Queue, Message_Backbone ou processo, THE AI_Agent_Orchestrator SHALL propagar o Trace_Context correspondente.
4. WHEN uma Task, Subtask, Attempt, Tool_Call ou chamada ao Provider_LLM emitir Structured_Log, THE Observability_Service SHALL incluir Trace_ID e os identificadores disponíveis entre Task_ID, Subtask_ID, Attempt_ID e Tool_Call_ID.
5. THE Observability_Service SHALL restringir labels de métricas a conjuntos configurados de cardinalidade limitada.
6. IF um identificador de alta cardinalidade não estiver permitido como label de métrica, THEN THE Observability_Service SHALL manter o identificador fora do label e preservar o identificador somente em traces ou Structured_Logs redigidos.
7. THE Observability_Service SHALL expor métricas de Tasks por estado, duração, filas, workers ativos, Backpressure, Attempts, timeouts, cancelamentos, Tool_Calls, chamadas ao Provider_LLM e falhas por classificação.
8. WHEN um panic ocorrer em uma goroutine controlada, THE Worker_Pool SHALL recuperar a fronteira da unidade de trabalho, registrar stack trace redigido e classificar o Attempt como falha.
9. IF telemetria externa estiver indisponível, THEN THE Observability_Service SHALL limitar buffers locais pelos Reference_Limits e preservar o processamento de negócio.
10. IF um buffer de telemetria alcançar a capacidade configurada, THEN THE Observability_Service SHALL aplicar a política configurada de descarte ou amostragem e contabilizar a perda sem bloquear Worker_Pools.
11. WHEN uma Task terminar, THE Observability_Service SHALL emitir uma métrica terminal e um span final com Task_Status.
12. IF um campo estiver classificado como segredo ou dado sensível, THEN THE Observability_Service SHALL aplicar redação antes da exportação.
13. IF a exportação de telemetria falhar, THEN THE Observability_Service SHALL registrar a condição sem incluir o payload sensível que causou a falha.

### Requirement 24: Saúde operacional

**User Story:** Como plataforma de execução, quero sinais de saúde sem dados sensíveis, para automatizar diagnóstico e admissão.

#### Acceptance Criteria

1. WHEN um cliente solicitar `GET /api/v1/health`, THE API_Service SHALL retornar Liveness, Readiness, versão e estado agregado das dependências obrigatórias sem segredos.
2. WHILE o processo puder responder, THE Liveness SHALL permanecer disponível independentemente da saúde de dependências externas.
3. WHILE o processo estiver vivo e as dependências obrigatórias estiverem prontas, THE API_Service SHALL responder ao endpoint de saúde com HTTP `200`.
4. IF uma dependência obrigatória impedir novas Tasks, THEN THE API_Service SHALL responder ao endpoint de saúde com HTTP `503` e um código de componente estável.
5. IF uma dependência opcional estiver degradada sem impedir admissão, THEN THE API_Service SHALL responder com HTTP `200` e marcar o componente como degradado.
6. WHEN a saúde de uma dependência for verificada, THE API_Service SHALL aplicar timeout finito definido nos Reference_Limits.
7. IF a verificação de uma dependência obrigatória exceder o timeout, THEN THE API_Service SHALL classificar a dependência como indisponível para Readiness.
8. IF a verificação de uma dependência opcional exceder o timeout, THEN THE API_Service SHALL classificar a dependência como degradada.
9. WHILE migração ou validação obrigatória de inicialização estiver incompleta, THE Readiness SHALL indicar indisponibilidade.
10. THE API_Service SHALL omitir endereços internos, credenciais, payloads de erro remotos, stack traces e dados de Tasks no endpoint de saúde.

### Requirement 25: Graceful shutdown

**User Story:** Como operador, quero encerrar processos sem perder trabalho aceito, para realizar deploys e manutenção previsíveis.

#### Acceptance Criteria

1. WHEN o processo receber sinal de encerramento, THE Shutdown_Manager SHALL interromper atomicamente a admissão de novas Tasks antes de iniciar drain ou cancelamento.
2. WHEN a admissão for interrompida, THE Readiness SHALL indicar indisponibilidade.
3. WHILE o Graceful_Shutdown estiver ativo, THE API_Service SHALL responder a novas submissões com HTTP `503` e `Retry-After`.
4. WHEN sinais de encerramento adicionais forem recebidos durante Graceful_Shutdown, THE Shutdown_Manager SHALL reutilizar o encerramento em andamento sem iniciar outro fluxo concorrente.
5. WHEN o Graceful_Shutdown iniciar, THE Shutdown_Manager SHALL drenar ou cancelar Worker_Pools conforme a política configurada e o Grace_Period.
6. WHEN uma unidade de trabalho terminar durante o Grace_Period, THE PostgreSQL_Store SHALL persistir o resultado antes do fechamento da dependência correspondente.
7. IF o Grace_Period expirar com trabalho em voo, THEN THE Shutdown_Manager SHALL marcar os Attempts ativos como recuperáveis no PostgreSQL_Store com Task_ID, Subtask_ID, identificador idempotente e expiração do Lease.
8. WHEN um producer não puder aceitar novo trabalho, THE Shutdown_Manager SHALL interromper o producer antes de fechar o channel consumido correspondente.
9. WHEN todos os producers de um channel encerrarem, THE Shutdown_Manager SHALL fechar o channel exatamente uma vez.
10. WHEN o trabalho estiver drenado ou recuperável, THE Shutdown_Manager SHALL fechar conexões e descarregar telemetria dentro de timeout finito.
11. IF uma persistência obrigatória falhar durante o encerramento, THEN THE Shutdown_Manager SHALL deixar o trabalho sem Acknowledgement e elegível para recuperação.
12. IF um novo processo recuperar trabalho interrompido pelo shutdown, THEN THE Orchestrator SHALL criar uma nova Delivery_Attempt sem duplicar o resultado confirmado.

### Requirement 26: Configuração, Docker e ambiente reproduzível

**User Story:** Como desenvolvedor, quero executar a plataforma localmente com dependências reais, para demonstrar o sistema e reproduzir testes de integração.

#### Acceptance Criteria

1. THE Docker_Compose_Environment SHALL iniciar os processos da aplicação, PostgreSQL_Store, Redis_Service e componentes locais obrigatórios de observabilidade.
2. WHEN o Docker_Compose_Environment atingir Readiness, THE API_Service SHALL aceitar uma Task de demonstração capaz de usar pelo menos uma Real_Tool de cada especialização.
3. THE AI_Agent_Orchestrator SHALL carregar configuração por arquivo ou variáveis de ambiente conforme um JSON_Schema ou contrato tipado versionado.
4. THE AI_Agent_Orchestrator SHALL documentar uma única ordem determinística de precedência entre fontes de configuração.
5. WHEN o mesmo conjunto de fontes de configuração for carregado, THE AI_Agent_Orchestrator SHALL produzir a mesma configuração efetiva conforme a precedência documentada.
6. WHEN duas fontes definirem a mesma chave, THE AI_Agent_Orchestrator SHALL selecionar o valor da fonte de maior precedência e identificar a origem sem registrar segredo.
7. IF um valor obrigatório estiver ausente, possuir tipo inválido ou violar uma relação entre campos, THEN THE AI_Agent_Orchestrator SHALL falhar a validação de inicialização com um código estável que não inclua segredo.
8. IF um valor de Reference_Limits estiver ausente, não positivo ou inconsistente, THEN THE AI_Agent_Orchestrator SHALL falhar a inicialização com um código de configuração e sem expor segredo.
9. WHEN o PostgreSQL_Store iniciar com schema desatualizado, THE AI_Agent_Orchestrator SHALL executar migrações versionadas antes de habilitar Readiness.
10. WHILE uma migração estiver em execução, THE AI_Agent_Orchestrator SHALL impedir execução concorrente incompatível da mesma versão de migração.
11. IF uma migração falhar, THEN THE AI_Agent_Orchestrator SHALL manter Readiness indisponível e registrar o erro sem marcar a versão como aplicada.
12. WHERE Local_Development_Mode estiver ativo, THE Docker_Compose_Environment SHALL fornecer dados e fontes de demonstração sem representar mocks como Real_Tools.
13. WHEN uma imagem de aplicação for construída a partir das mesmas fontes e dependências fixadas, THE AI_Agent_Orchestrator SHALL produzir um binário Go reproduzível.
14. WHEN o contêiner da aplicação iniciar, THE AI_Agent_Orchestrator SHALL executar com usuário sem privilégios administrativos.
15. THE AI_Agent_Orchestrator SHALL manter segredos de runtime fora das camadas da imagem de aplicação.

### Requirement 27: Qualidade e testes

**User Story:** Como mantenedor, quero uma suíte que exerça lógica, integrações e concorrência, para evoluir o sistema com confiança.

#### Acceptance Criteria

1. THE AI_Agent_Orchestrator SHALL incluir testes unitários para Task_State_Machine, Subtask_State_Machine, validação do Planner, Scheduler, Retry_Policy, autorização, limites e agregação determinística.
2. THE AI_Agent_Orchestrator SHALL incluir testes de API para JSON malformado, medição real de payload, falha de persistência e concorrência de Idempotency_Key.
3. THE AI_Agent_Orchestrator SHALL incluir testes de transição para repetição idempotente, disputa concorrente, estado terminal e consistência entre estado e Task_Event.
4. THE AI_Agent_Orchestrator SHALL incluir testes das Tools para parâmetros inválidos, Allowlist, canonização, zero resultados, truncamento, indisponibilidade e rastreabilidade.
5. THE AI_Agent_Orchestrator SHALL incluir testes de autorização para a precedência entre HTTP `401`, `403` e `404`, propriedade, Scope, Role administrativa e Local_Development_Mode.
6. THE AI_Agent_Orchestrator SHALL incluir testes de Event_Stream para replay inicial, retomada, cursor inválido, cursor expirado, ordenação, cliente lento e credencial expirada.
7. THE AI_Agent_Orchestrator SHALL incluir testes de persistência e recuperação para Leases, redelivery, Acknowledgement inválido, Redis_Service vazio e trabalho confirmado antes de crash.
8. THE AI_Agent_Orchestrator SHALL incluir testes de integração de API_Service, PostgreSQL_Store, Redis_Service e migrações com dependências reais descartáveis.
9. WHERE uma imagem de contêiner versionada e um health check determinístico existirem para a dependência, THE AI_Agent_Orchestrator SHALL usar Testcontainers nos testes de integração em lugar de simulação do protocolo da dependência.
10. THE AI_Agent_Orchestrator SHALL incluir testes de concorrência para Worker_Pools, Bounded_Queues, Backpressure, fairness, contabilização de slots, cancelamento, timeouts, recuperação e Graceful_Shutdown.
11. WHEN a suíte Go for executada com o Race_Detector, THE AI_Agent_Orchestrator SHALL concluir os testes com zero data races reportadas.
12. WHEN falhas forem injetadas em Specialized_Agent, Provider_LLM, Tool, PostgreSQL_Store, Redis_Service e Asynchronous_Dispatch, THE AI_Agent_Orchestrator SHALL verificar os estados, eventos e fallbacks previstos neste documento.
13. WHEN contratos JSON forem alterados, THE AI_Agent_Orchestrator SHALL verificar compatibilidade com a versão de API declarada por testes de contrato.
14. WHEN geradores produzirem Execution_Plans válidos, THE AI_Agent_Orchestrator SHALL verificar por propriedades a unicidade de Subtask_ID, aciclicidade, respeito aos Reference_Limits e round trip do LLM_Response_Parser e LLM_Response_Printer.
15. WHEN geradores produzirem consultas fora do subconjunto permitido, THE AI_Agent_Orchestrator SHALL verificar por propriedades a rejeição pelo SQL_Query_Validator sem execução no PostgreSQL_Store.
16. WHEN geradores produzirem ordens diferentes dos mesmos Normalized_Results, THE AI_Agent_Orchestrator SHALL verificar a equivalência semântica do fallback determinístico.
17. WHEN um teste de concorrência cancelar o Execution_Context raiz, THE AI_Agent_Orchestrator SHALL verificar o encerramento de todas as goroutines de trabalho controladas em até 2 segundos.
18. WHEN uma implementação candidata for validada, THE AI_Agent_Orchestrator SHALL executar `gofmt`, análise estática, testes unitários, testes de integração, testes de propriedades, testes de contrato e `go test -race` em pipeline não interativo.
19. IF qualquer etapa obrigatória do pipeline falhar ou não for executada, THEN THE AI_Agent_Orchestrator SHALL marcar o pipeline como falho sem publicar resultado de sucesso.

### Requirement 28: Implementação Go idiomática e evolução incremental

**User Story:** Como mantenedor, quero uma base Go simples e explícita, para demonstrar competências reais sem burocracia arquitetural.

#### Acceptance Criteria

1. THE AI_Agent_Orchestrator SHALL usar `context.Context` como Execution_Context para cancelamento e prazos em fronteiras bloqueantes.
2. WHEN uma função bloquear, iniciar I/O ou criar trabalho concorrente, THE AI_Agent_Orchestrator SHALL receber ou derivar um Execution_Context com proprietário e prazo verificáveis.
3. THE AI_Agent_Orchestrator SHALL usar goroutines, channels e Worker_Pools limitados para a concorrência descrita neste documento.
4. IF uma goroutine for criada para trabalho controlado, THEN THE AI_Agent_Orchestrator SHALL definir proprietário, condição de término, caminho de cancelamento e mecanismo de espera verificáveis.
5. WHEN um channel for criado, THE AI_Agent_Orchestrator SHALL atribuir a propriedade de fechamento ao producer responsável.
6. WHEN o producer responsável encerrar todos os envios, THE AI_Agent_Orchestrator SHALL fechar o channel exatamente uma vez.
7. WHILE qualquer producer autorizado ainda puder enviar, THE AI_Agent_Orchestrator SHALL manter o channel correspondente aberto.
8. THE AI_Agent_Orchestrator SHALL limitar interfaces a fronteiras externas, pontos com pelo menos duas implementações de produção ou dependências externas que exigem substituição determinística em testes.
9. THE AI_Agent_Orchestrator SHALL organizar packages por capacidades coesas e fronteiras de runtime com fluxo de controle direto entre packages adjacentes.
10. THE AI_Agent_Orchestrator SHALL manter o grafo de imports de packages acíclico.
11. WHEN um erro atravessar uma fronteira interna, THE AI_Agent_Orchestrator SHALL adicionar contexto preservando a causa para inspeção por `errors.Is` ou `errors.As`.
12. WHEN um erro alcançar uma fronteira pública, THE AI_Agent_Orchestrator SHALL converter o erro para classificação estável sem expor detalhes internos sensíveis.
13. WHEN uma nova especialização ou Tool for adicionada, THE AI_Agent_Orchestrator SHALL permitir registro no Agent_Registry e no Tool_Gateway sem alterar contratos públicos existentes da Task.
14. WHEN limites de concorrência ou integrações externas mudarem, THE AI_Agent_Orchestrator SHALL aceitar a evolução por configuração ou extensão localizada sem alterar a semântica de Task_Status.
15. WHEN o código for submetido ao pipeline, THE AI_Agent_Orchestrator SHALL atender a `gofmt`, `go vet` e ao Race_Detector.

## Decisões obrigatórias para a fase de design

Esta seção registra ambiguidades deliberadamente não resolvidas nesta fase. As decisões não autorizam a criação do design antes da aprovação deste documento.

### D1. Protocolo do Event_Stream

A necessidade atual é unidirecional, com retomada, Event_ID, heartbeat, autenticação e Backpressure. A recomendação inicial é **SSE**, por usar HTTP, reconexão simples e aderência ao endpoint `GET`. **WebSocket** deve ser escolhido somente se a fase de design demonstrar necessidade de comandos bidirecionais de baixa latência na mesma conexão. A escolha deve comparar: retomada, semântica de ordenação, suporte de proxies, autenticação, Backpressure, custo operacional, observabilidade e experiência do cliente Go/web.

### D2. Message_Backbone

A fase de design deve avaliar **Redis Streams**, **NATS com JetStream** e **RabbitMQ**. A decisão deve aplicar estes critérios e pesos iniciais:

| Critério | Peso |
|---|---:|
| Durabilidade, acknowledgements, redelivery e recuperação após crash | 25% |
| Simplicidade operacional e integração com Docker Compose | 20% |
| Consumer groups, Backpressure, retry e estratégia de dead letter | 20% |
| Escala horizontal, throughput e latência sob Reference_Limits | 15% |
| Observabilidade e diagnóstico operacional | 10% |
| Maturidade, manutenção e ergonomia do cliente Go | 10% |

São critérios eliminatórios: entrega pelo menos uma vez, reconhecimento explícito, redelivery, backlog limitado, coordenação entre consumidores, operação local reproduzível e cliente Go mantido. A reutilização do Redis_Service favorece Redis Streams em simplicidade, mas não determina a escolha. O design também pode justificar uma primeira iteração baseada no PostgreSQL_Store, desde que compare explicitamente o custo de migração e preserve todos os requisitos de Asynchronous_Dispatch.

### D3. Identidade e isolamento organizacional

A premissa inicial é Single_Organization_Mode com bearer tokens de uma autoridade OIDC configurável, Roles, Scopes e propriedade por Principal. Antes da implementação devem ser confirmados: autoridade de identidade, formato de identidades de serviço, Roles iniciais e necessidade de isolamento multi-tenant futuro.

### D4. Valores padrão de retenção, capacidade e orçamento

Antes da implementação devem ser aprovados valores padrão para: retenção de histórico, Tasks concorrentes, Subtasks por Task, workers globais e por Task, capacidades de filas e buffers de eventos, duração máxima de Task, timeouts, Attempts, Tool_Calls, bytes de saída, tokens, recarga de segredos, limite emergencial por processo e limites de API. Os comportamentos quando cada limite é atingido já são normativos neste documento.

### D5. Descoberta de histórico

A primeira versão proposta consulta histórico somente por `Task_ID`. Caso usuários precisem descobrir Tasks anteriores, deve ser adicionado requisito para `GET /api/v1/tasks` com paginação, filtros, ordenação, isolamento por Owner e política de retenção antes do design.

### D6. Aprovação humana

A primeira versão proposta permite autonomia depois da autenticação e da autorização de Tools somente leitura. Caso alguma fonte exija aprovação por Tool_Call, devem ser adicionados estados, expiração, API de aprovação e comportamento de cancelamento antes do design.

### D7. Fontes reais da demonstração

Antes do design final devem ser escolhidas as fontes executáveis de demonstração para logs, banco, código e infraestrutura. Cada escolha deve possuir contrato somente leitura, fixture reproduzível para Docker_Compose_Environment e estratégia de integração sem credenciais reais no repositório.

## Matriz de endpoints iniciais

| Método e caminho | Scope mínimo | Resultado principal |
|---|---|---|
| `POST /api/v1/tasks` | `tasks:create` | Aceita uma Task assíncrona |
| `GET /api/v1/tasks/{id}` | `tasks:read` + Owner/admin | Retorna estado, progresso e resultado persistido |
| `POST /api/v1/tasks/{id}/cancel` | `tasks:cancel` + Owner/admin | Registra cancelamento idempotente |
| `GET /api/v1/tasks/{id}/events` | `tasks:read` + Owner/admin | Abre Event_Stream retomável |
| `GET /api/v1/agents` | `agents:read` | Lista capacidades e saúde dos agentes |
| `GET /api/v1/health` | política operacional configurada | Retorna Liveness e Readiness sem dados sensíveis |

## Rastreabilidade de objetivos posteriores

Após a aprovação dos requisitos, a fase de design deverá tratar explicitamente: arquitetura e componentes; modelo de dados; fluxo ponta a ponta; modelo de concorrência; escolha de Event_Stream e Message_Backbone; políticas de segurança; observabilidade; riscos e mitigação; estratégia de testes; implantação; evolução incremental e roadmap. Nenhum desses artefatos é produzido nesta fase.
