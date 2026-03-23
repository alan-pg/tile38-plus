# Guia de Arquitetura do Tile38 (Fork)

Este guia documenta a arquitetura do **Tile38** e fornece instruções sobre como modificar funcionalidades existentes e criar novas funcionalidades no nosso fork.

## Visão Geral da Arquitetura

O **Tile38** é um banco de dados em memória, otimizado para dados geoespaciais, buscas espaciais e geofencing em tempo real. Ele foi desenvolvido totalmente em **Go** e possui suporte a múltiplos protocolos e interfaces de comunicação.

### Estrutura de Diretórios Principal

A base de código está bem dividida logicamente. Aqui estão os diretórios cruciais do projeto:

- `cmd/`: Contém os pontos de entrada dos aplicativos compilados.
  - `tile38-server/`: O executável principal do servidor.
  - `tile38-cli/`: Uma ferramenta de linha de comando para interagir via RESP (como o redis-cli).
  - `tile38-benchmark/`: Ferramenta para testes de carga e performance.
- `core/`: Contém definições e metadados centrais que não são lógicas de servidor puro. O principal arquivo de interesse aqui é `commands.json`, que registra todos os comandos que o servidor aceita.
- `internal/`: O núcleo funcional do projeto. Todo o coração arquitetural reside aqui.
  - `internal/server/`: Lógica central do servidor Tile38. Ele gerencia as conexões, processa o protocolo RESP, despacha os comandos e interage com o Append-Only File (AOF).
  - `internal/collection/`: A engine do banco. Aqui os dados são armazenados na memória. Utiliza as bibliotecas B-Tree (para buscas rápidas por ID) e R-Tree (para buscas espaciais rápidas).
  - `internal/endpoint/`: Gerencia o envio de webhooks e notificações de pub/sub e geofencing. Oferece suporte nativo a múltiplos "sinks" (HTTP, Redis, Kafka, NATS, AWS SQS, AMQP, etc).
  - `internal/object/`: Define representações geográficas, lidando primariamente com conversões GeoJSON.
  - `internal/glob/`: Usado para dar "match" avançado nas seleções de chaves (como o padrão wildcard * no comando KEYS).

---

## Criando ou Modificando Funcionalidades

### 1. Adicionando um Novo Comando Customizado

Se o objetivo for criar um comando novo (exemplo: `MYCOMMAND`), siga estes passos:

1. **Defina o Comando no Core**:
   Vá até o arquivo `core/commands.json` e adicione a declaração estrutural dele. Forneça o summary, arguments e a complexidade.
   *Atenção: Modificar o `commands.json` geralmente requer rodar `go generate` na pasta `core` ou o script `gen.sh` para regenerar `commands_gen.go`*.

2. **Implemente a Lógica do Comando**:
   Vá em `internal/server/`, crie ou abra um arquivo apropriado para a sua lógica lógica (ex: `crud.go` para manipulações, `search.go` para buscas, etc.).
   Crie uma função seguindo este padrão (os retornos padrão na engine envolvem Response Value, Command Details e Error):
   ```go
   func (s *Server) cmdMYCOMMAND(msg *Message) (resp.Value, commandDetails, error) {
       // Sua logica aqui
       return resp.SimpleStringValue("OK"), commandDetails{}, nil
   }
   ```
   *(Pode diferir levemente se for um comando read-only ou write-heavy. Você pode olhar `cmdSET` como referência de 'write', e `cmdGET` de 'read'.)*

3. **Registre o Comando no Dispatcher**:
   Abra `internal/server/server.go`. Localize a função `func (s *Server) command(...)`. Dentro dela, existe um grande `switch msg.Command()`:
   Adicione o caso do seu novo comando para rotear a chamada para a função que você criou:
   ```go
   case "mycommand":
       res, d, err = s.cmdMYCOMMAND(msg)
   ```

4. **Gerenciamento de Lock**:
   No mesmo `server.go`, existe um switch antes do dispatching *("choose the locking strategy")*. Você precisará declarar se o comando faz "Mutate" (`s.mu.Lock()`) ou se é Read-Only (`s.mu.RLock()`). Se ele alterar dados (como `set`, `del`), ele obrigatoriamente estará sob Writer Lock para que essas alterações fluam em modo ACID para o AOF e réplicas.

### 2. Adicionando um Novo Protocolo/Endpoint de Notificação (Webhook)

O Tile38 oferece integrações maravilhosas para envio de "Geofences" em tempo real em várias filas. Caso queiramos adicionar integração com uma tecnologia proprietária nossa ou não existente (exemplo: `S3`, `Twilio`, etc):

1. **Defina a Estrutura do Novo Endpoint**:
   Vá em `internal/endpoint/` e crie um arquivo novo, como `myprotocol.go`.

2. **Crie o Conector**:
   Implemente a inicialização do seu endpoint para que ele retorne uma struct que referencie seu protocolo.

3. **Implemente a interface `Conn`**:
   O arquivo deverá exportar a interface `Conn`, que possui o método primário `.Send(val string) error`. É aqui que vai a lógica de disparar a mensagem GeoJSON do Tile38 para o serviço externo.

4. **Registre sua URL Customizada**:
   Em `internal/endpoint/endpoint.go`, modifique a função `parseEndpoint(s string) (Endpoint, error)`. É lá que a URI (ex: `myprotocol://`) é mapeada na struct `Endpoint`. Ao rodar `SETHOOK myhook myprotocol://token... NEARBY...`, o parser reconhecerá.

### 3. Alterando Funcionalidades Relacionadas ao Espaço ou Busca (Geofencing / RTree)

1. Funcionalidades espaciais (calculo de sobreposição, bounds, radius radius, etc.) ocorrem no pacote `internal/collection`.
2. Se quiser intervir antes da resposta de uma Geofence, dê uma olhada em `internal/server/fence.go`. É a engine vital de detecção "cross," "enter," "exit", "inside", "outside", baseada em iterar os bounds atuais versus os novos de um objeto recém mutado.

### Recomendações Extras e Boas Práticas

- **Testes**: Todo comando/funcionalidade adicionada deve preferencialmente receber suporte na bateria nativa via interface Redis no diretório raiz ou `/internal/server/`. Utilize `go test ./...` para lidar confiantemente com a concorrência.
- O Tile38 gerencia paralelismo pesado via Goroutines baseadas em RWMutex. O Lock/Unlock precisa ser estrito na declaração do Switch.
- Arquiteturalmente, evite mexer muito na lib `tidwall/rtree` base, deixe a manipulação em nível abstrato de `collection.Collection` a menos que precise de uma mudança violenta de performance e índices n-dimensionais.
