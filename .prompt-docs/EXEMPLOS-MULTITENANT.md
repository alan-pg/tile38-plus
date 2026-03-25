# Exemplos de Teste - Sistema Multitenant e Limite de Velocidade no Tile38-Plus

Este documento servirá como um guia prático para testar as funcionalidades do sistema multitenant e os recursos exclusivos do Tile38-Plus, como controle de velocidade em cercas (`SPEEDLIMIT`), filtro de pacotes gagos/atrasados (`NEWER`) e a arquitetura de isolamento baseada em `MATCH` e coleção única.

## Acesso Prático a Partir do Node.js (Recomendação)

Para acessar o Tile38 usando uma aplicação Node.js (ou TS), a **melhor biblioteca (recomendação oficial da comunidade)** é o **`ioredis`**, pois o Tile38 usa o protocolo nativo RESP.

O `ioredis` enviará os comandos de geolocalização como Array de strings usando o método genérico `.call()`.

```javascript
/* npm install ioredis */
import Redis from 'ioredis';

const tile38 = new Redis({ host: '127.0.0.1', port: 9851 });
```

## Arquitetura Multitenant (Padrão de Chaves)

Para suportar múltiplas empresas (multitenancy), grupos de veículos e veículos específicos dentro de uma única coleção unificada, utilizaremos um padrão de hierarquia nas identificações (IDs), o que permite tirar proveito da altíssima performance de processamento léxico do comando `MATCH` do Tile38.

**Coleção Padrão:** `fleet`
**Padrão de ID do Veículo:** `empresa_id:grupo_id:veiculo_id`

Exemplos Práticos:
- `empresaA:logistica:caminhao01`
- `empresaA:diretoria:carro_sedan`
- `empresaB:vendas:carro_joao`

---

## 1. Criação de Cercas Multitenant (Fences)

Para criar as cercas em memória, usaremos `SETCHAN` (para visualizar localmente) ou `SETHOOK` (para endpoints HTTP). O recurso `MATCH <padrao>` fará o isolamento de quais veículos ativam aquela cerca.
Definiremos no escopo do `DETECT` monitoramento total incluindo transições de velocidade.

### A. Cerca Global para Todos os Veículos de uma Empresa
Monitora SOMENTE a **Empresa A** (`MATCH empresaA:*`). O limite de velocidade avaliado será de **80 km/h** focado no campo `speed`.

**Em CLI:**
```bash
SETCHAN fence_empresaA_geral WITHIN fleet FENCE MATCH empresaA:* DETECT enter,exit,overspeed,underspeed SPEEDLIMIT 80 speed BOUNDS -23.6 -46.7 -23.4 -46.5
```

**Em Node.js:**
```javascript
await tile38.call('SETCHAN', 'fence_empresaA_geral', 'WITHIN', 'fleet', 'FENCE', 'MATCH', 'empresaA:*', 'DETECT', 'enter,exit,overspeed,underspeed', 'SPEEDLIMIT', 80, 'speed', 'BOUNDS', -23.6, -46.7, -23.4, -46.5);
```

### B. Cerca para um Grupo Específico de Veículos da Empresa
Monitora SOMENTE o grupo **logistica** da **Empresa A** (`MATCH empresaA:logistica:*`). O limite desta cerca será mais rigoroso, de **60 km/h**.

```bash
SETCHAN fence_empresaA_logistica WITHIN fleet FENCE MATCH empresaA:logistica:* DETECT enter,exit,overspeed,underspeed SPEEDLIMIT 60 speed BOUNDS -23.6 -46.7 -23.4 -46.5
```

### C. Cerca para um Veículo Específico
Monitora EXCLUSIVAMENTE o veículo **caminhao01** do grupo logistica da **Empresa A** (`MATCH empresaA:logistica:caminhao01`). Limite específico de **90 km/h**.

```bash
SETCHAN fence_empresaA_caminhao01 WITHIN fleet FENCE MATCH empresaA:logistica:caminhao01 DETECT overspeed,underspeed SPEEDLIMIT 90 speed BOUNDS -23.6 -46.7 -23.4 -46.5
```

### D. Cerca de Velocidade Global (Sem Fronteiras Geográficas)
Cria uma cerca monitorando os limites em todo o planeta (`BOUNDS -90 -180 90 180`). Esse tipo de cerca ignora os limites espaciais e ativa eventos unicamente baseados na velocidade a qual quer nível (Neste caso ativado sempre que a van bater **110 km/h** em QUALQUER LUGAR).

```bash
SETCHAN global_limit_empresaA WITHIN fleet FENCE MATCH empresaA:* DETECT overspeed,underspeed SPEEDLIMIT 110 speed BOUNDS -90 -180 90 180
```

---

## 2. Envio de Dados de Localização e Telemetria

Os dados inseridos no Tile38 agora levam os `FIELD`s que armazenam detalhes telemétricos vitais (como a velocidade e tempo na máquina no momento da geração - usado no `NEWER`). O comando `NEWER` protege contra ordenação incorreta de mensagens GPS de rede.

### Evento 1: Veículo entra na área dentro das margens (Normal)
*Veículo: caminhao01 (Empresa A, Grupo logistica)*

**Em CLI:**
```bash
# Entrando na cerca a 50 km/h. 
# Resultado: Acionará o evento espacional padrão "enter" em `fence_empresaA_geral` e `fence_empresaA_logistica`.
SET fleet empresaA:logistica:caminhao01 FIELD speed 50 FIELD timestamp 1700000001 POINT -23.5 -46.6
```

**Em Node.js:**
```javascript
// O "NEWER timestamp" está na declaração para evitar conflitos na primeira gravação.
await tile38.call('SET', 'fleet', 'empresaA:logistica:caminhao01', 'FIELD', 'speed', 50, 'NEWER', 'timestamp', 'FIELD', 'timestamp', 1700000001, 'POINT', -23.5, -46.6);
```

### Evento 2: Veículo quebra o limite do Grupo (60 km/h) mas está abaixo do Geral (80 km/h)
*Veículo: caminhao01 (Empresa A, Grupo logistica)*

**Em CLI:**
```bash
# Movendo-se a 70 km/h. Protegido ativamente com NEWER contra pacotes atrasados da fila.
# Resultado: Acionará "overspeed" APENAS na `fence_empresaA_logistica`.
SET fleet empresaA:logistica:caminhao01 FIELD speed 70 NEWER timestamp FIELD timestamp 1700000002 POINT -23.51 -46.61
```

**Em Node.js:**
```javascript
await tile38.call('SET', 'fleet', 'empresaA:logistica:caminhao01', 'FIELD', 'speed', 70, 'NEWER', 'timestamp', 'FIELD', 'timestamp', 1700000002, 'POINT', -23.51, -46.61);
```

### Evento 3: Veículo quebra o limite Geral (80 km/h)
*Veículo: caminhao01 (Empresa A, Grupo logistica)*

**Em CLI:**
```bash
# Movendo-se a 85 km/h. 
# Resultado: Acionará "overspeed" na `fence_empresaA_geral`. Nenhuma dupla-emissão ocorrerá para o Grupo Logística porque ele já estava acima do limite na notificação passada.
SET fleet empresaA:logistica:caminhao01 FIELD speed 85 NEWER timestamp FIELD timestamp 1700000003 POINT -23.52 -46.62
```

**Em Node.js:**
```javascript
await tile38.call('SET', 'fleet', 'empresaA:logistica:caminhao01', 'FIELD', 'speed', 85, 'NEWER', 'timestamp', 'FIELD', 'timestamp', 1700000003, 'POINT', -23.52, -46.62);
```

### Evento 4: Veículo reduz a velocidade - Normalização Total
*Veículo: caminhao01 (Empresa A, Grupo logistica)*

**Em CLI:**
```bash
# Reduz para 55 km/h. 
# Resultado: Acionará "underspeed" simultâneo nas duas cercas `fence_empresaA_geral` e `fence_empresaA_logistica`.
SET fleet empresaA:logistica:caminhao01 FIELD speed 55 NEWER timestamp FIELD timestamp 1700000004 POINT -23.53 -46.63
```

**Em Node.js:**
```javascript
await tile38.call('SET', 'fleet', 'empresaA:logistica:caminhao01', 'FIELD', 'speed', 55, 'NEWER', 'timestamp', 'FIELD', 'timestamp', 1700000004, 'POINT', -23.53, -46.63);
```

### Evento 5: Simulação de descarte ativo via NEWER
*Veículo: caminhao01 (Empresa A, Grupo logistica)*

**Em CLI:**
```bash
# Um pacote antigo travado no MQ foi entregue (timestamp 1700000000). A marcação atual é 1700000004.
# Resultado: O pacote será completamente ignorado silênciosamente pelo Tile38. Fences não reagirão ao dado defasado.
SET fleet empresaA:logistica:caminhao01 FIELD speed 99 NEWER timestamp FIELD timestamp 1700000000 POINT -23.5 -46.6
```

**Em Node.js:**
```javascript
// Retornará Int(0), sinalizando que o Tile38 ignorou essa localização desordenada.
const blockReason = await tile38.call('SET', 'fleet', 'empresaA:logistica:caminhao01', 'FIELD', 'speed', 99, 'NEWER', 'timestamp', 'FIELD', 'timestamp', 1700000000, 'POINT', -23.5, -46.6);
console.log(blockReason === 0 ? "Pacote IGNORADO" : "Pacote Aceito");
```

---

## 3. Testando o Isolamento Multitenant Inquebrável (Empresa B)

Para garantir segurança total no isolamento via `MATCH`, testaremos a **Empresa B** passando na mesma rota geográfica sem disparar callbacks da Empresa A.

### Criação de Cerca Geral - Empresa B

**Em CLI:**
```bash
# Cerca exclusiva da Empresa B (Limite 100 km/h) no mesmo espaço
SETCHAN fence_empresaB_geral WITHIN fleet FENCE MATCH empresaB:* DETECT enter,exit,overspeed,underspeed SPEEDLIMIT 100 speed BOUNDS -23.6 -46.7 -23.4 -46.5
```

**Em Node.js:**
```javascript
await tile38.call('SETCHAN', 'fence_empresaB_geral', 'WITHIN', 'fleet', 'FENCE', 'MATCH', 'empresaB:*', 'DETECT', 'enter,exit,overspeed,underspeed', 'SPEEDLIMIT', 100, 'speed', 'BOUNDS', -23.6, -46.7, -23.4, -46.5);
```

### Evento 6: Isolamento na Prática

**Em CLI:**
```bash
# Empresa B - Van entra na cerca comum a absurdos 110 km/h
# Resultado: Aciona "enter" e imediatamente "overspeed" EXCLUSIVO no canal `fence_empresaB_geral`. Fences da Empresa A ficam cegas à Van da Empresa B.
SET fleet empresaB:vendas:van01 FIELD speed 110 FIELD timestamp 1700000005 POINT -23.51 -46.61
```

**Em Node.js:**
```javascript
await tile38.call('SET', 'fleet', 'empresaB:vendas:van01', 'FIELD', 'speed', 110, 'FIELD', 'timestamp', 1700000005, 'POINT', -23.51, -46.61);
```
