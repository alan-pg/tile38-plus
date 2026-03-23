# Proposta de Implementação: Geofence com Limite de Velocidade

O objetivo é permitir que uma geofence (cerca virtual) monitore a velocidade de um objeto e emita eventos específicos caso a velocidade seja excedida ou volte ao normal.

## Análise do Estado Atual (Tile38)

Atualmente, o Tile38 **não armazena o timestamp de atualização** dentro da estrutura do objeto na memória (ver `internal/object/object_binary.go`). Apenas a geometria, o tempo de expiração (`TTL`) e os "fields" suplementares são guardados para economizar memória (já que o Tile38 é *in-memory*).

Além disso, as geofences operam comparando o estado do `obj` (novo objeto) com o `old` (objeto anterior antes do update) através da função `fenceMatch()` em `internal/server/fence.go`.

Para saber a velocidade de um objeto dentro de uma cerca, temos **duas abordagens possíveis**:

---

## Opção 1: Velocidade fornecida pelo usuário via `FIELD` (Recomendada)

Neste cenário, assumimos que o cliente que rastreia o objeto já calcula a velocidade ou lê diretamente do GPS, e envia isso no comando `SET`.

**Exemplo de uso pretendido:**
```
NEARBY frota FENCE SPEEDLIMIT 80 speed POINT -23.5 -46.6 6000
```
*(Onde `80` é o limite e `speed` é o nome do field onde a velocidade está armazenada)*

E a atualização do objeto seria:
```
SET frota caminhao1 FIELD speed 90 POINT -23.5 -46.6
```

### O que precisa ser modificado no código:

1. **`internal/server/token.go` (`searchScanBaseTokens`)**:
   - Adicionar os campos `speedLimit float64` e `speedField string` na struct para guardar esse token.
   - Modificar `parseSearchScanBaseTokens()` para fazer o parser das palavras-chave `SPEEDLIMIT <limite> <field>`.
   - Adicionar `overspeed` e `underspeed` ao mapa de detecções padrão da palavra-chave `DETECT`.

2. **`internal/server/fence.go` (`fenceMatch`)**:
   - Adicionar a lógica de processamento de velocidade. 
   - A lógica checará `sw.s.getFieldValue(details.old, fence.speedField)` versus `sw.s.getFieldValue(details.obj, fence.speedField)`.
   - Se a cerca configurou um limite de velocidade, e o objeto `old.speed <= limit` enquanto `obj.speed > limit`, geramos o evento `detect = "overspeed"`.
   - Se o objeto `old.speed > limit` mas agora `obj.speed <= limit`, geramos o evento `detect = "underspeed"`.

### Vantagens:
- Extrema performance. Sem impacto no uso geral de RAM do Tile38.
- O GPS cliente costuma ter medições de velocidade muito mais precisas sem depender do ping de rede gerando ruídos no cálculo de `∆espaço / ∆tempo`.

---

## Opção 2: Cálculo Dinâmico de Velocidade pelo Tile38

Nesta opção, o Tile38 calcula a velocidade sozinho descobrindo a distância percorrida dividida pelo tempo passado desde a última atualização.

**Exemplo de uso pretendido:**
```
NEARBY frota FENCE MAXSPEED 80 POINT -23.5 -46.6 6000
```
*(Se exceder 80 metros por segundo `m/s`, dispara)*

### O que precisa ser modificado no código:

1. **Problema do Timestamp**: O Tile38 não sabe quando a última coordenada (`old`) foi recebida. Precisamos forçar a persistência desse momento.
2. **`internal/object/object_binary.go`**:
   - Seria necessário modificar as representações binárias `geoObject` e `pointObject` para incluir 8-bytes extra de unix timestamp (`int64`), ou o Tile38 passará a salvar implicitamente um `FIELD _ts <nanosegundos>` ocultamente em todos os objetos (que causaria crescimento no footprint de RAM).
3. **`internal/server/fence.go` (`fenceMatch`)**:
   - Quando `MAXSPEED` estiver ativado:
   - `dist := details.obj.Geo().Distance(details.old.Geo())` (Distância em metros).
   - `timeDelta := details.timestamp.Sub(details.old.Timestamp).Seconds()`
   - `speed := dist / timeDelta`
   - Se a string `speed > MAXSPEED` e a anterior for <= gerará o evento `overspeed`. E vice-versa `underspeed`.

### Desvantagens:
- Para evitar bugs em saltos de GPS, as lógicas de filtro anti-jitter teriam de ser tratadas no Tile38.
- Aumento forçado de consumo de memória para todos os registros que precisariam gravar um timestamp obrigatoriamente.

---

## Eventos Disparados (`Webhook` / `PubSub`)

Independente da abordagem escolhida (Opção 1 ou 2), o payload gerado na saída deverá manter o padrão, adicionando o grupo de detecção correto:

**Exemplo de Payload Overspeed (Excedeu a Velocidade):**
```json
{
  "command": "set",
  "group": "XXXX",
  "detect": "overspeed",
  "hook": "cerca_velocidade_80",
  "key": "frota",
  "time": "2026-02-22T21:20:00-03:00",
  "id": "caminhao1",
  "object": {
    "type": "Point",
    "coordinates": [-46.6, -23.5]
  }
}
```

O webhook/pubsub cliente passará a receber esses pings perfeitamente. Desta forma, mantemos o Tile38 incrivelmente expansível semanticamente sem ferir a base estrita dele de alta disponibilidade.

> [!NOTE]
> A Opção 1 (Fornecimento de Velocidade via Field) é vastamente mais alinhada à forma com que as bibliotecas e arquitetura do Tile38 operam (onde a aplicação de tracking externa toma as decisões vitais e envia apenas estado como fields espaciais).
