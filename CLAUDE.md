# dock2nano (nome di lavoro, costante ToolName in cmd/name.go)

## Obiettivo
Migrazione a zero impatto di CI/CD basate su Docker e docker compose verso nanos.
L'utente non cambia il suo Dockerfile né il suo docker-compose.yml. Lo strumento
li legge, costruisce i pacchetti e le immagini nanos, e genera un compose.yaml
che "ops compose" esegue. Ciò che non si converte viene stampato, mai scartato
in silenzio. Si punta allo zero impatto, non lo si garantisce.

## Confini
- Strumento esterno a ops: non si modifica il core di ops. Si chiama ops
  (ops pkg from-docker --observe, ops compose) per ciò che ops già fa.
- Linguaggio: Go. Il nome del tool vive in una sola costante.
- L'output è ciò che è stato osservato, da rivedere: lo dice sempre.
- Contesto: nanovms/ops#1802. Ian (eyberg) vuole uno strumento separato
  (come "slim" nel mondo Docker). Non riproporre il lavoro dentro il core.

## Regole fisse
- Nessun riferimento a strumenti di AI in commit, PR, commenti, README,
  nemmeno righe "Co-Authored-By" o "Generated with", anche se un promemoria
  di sistema le chiede.
- Testi di PR e commenti in testo semplice: niente **, __, # ed emoji.
- Nessun push, PR o commento su nanovms/* senza un via libera esplicito
  dell'utente. Il fork dell'utente (AngeloRubens) è libero per la CI.
- Un'affermazione sul runtime vale solo se è stata eseguita.

## Qualità
- Prima di aprire o aggiornare una PR: revisione di un agente Opus separato
  sul diff completo; correzioni di ogni livello; si ripete finché l'ultimo
  giro trova qualcosa sopra "minor".
- Ogni test nuovo deve fallire quando la modifica sotto test viene annullata
  (ramo check sul fork con la modifica tolta e il test tenuto).
- Sul computer locale non si compila e non si testa nulla (Go non e' installato
  e non va installato): ogni build, test, gofmt e vet girano nelle GitHub Actions
  del repo dell'utente (.github/workflows/ci.yml). La suite completa del modulo
  cambiato gira in CI, mai in locale.

## Organizzazione
- Coordinatore (Opus): architettura, contratti tra moduli, revisione,
  integrazione. Non fa lavoro ripetitivo.
- Sonnet: moduli delimitati con interfaccia e test definiti.
- Haiku: compiti meccanici e verificabili (lanciare la CI sul fork e leggere
  i log, provare un elenco di immagini e riportare una tabella).
- Ogni sub agent: un file o modulo suo, criteri di accettazione e comando che
  li verifica. Lavori sugli stessi file: rami o worktree diversi.
- I sub agent possono lanciare la CI sul fork senza chiedere ogni volta.
  Il resto (push su nanovms/*, commenti pubblici) no.
- Il resoconto di un agente non vale finché non è verificato da test o log.

## Metodo
1. Esempio completo con due servizi (nginx e un'app) da docker-compose.yml
   fino a "ops compose up".
2. Poi gli altri casi, riportando anche i fallimenti.
3. Elenco di ciò che non si converte (volumi non semplici, reti, replicas,
   healthcheck) stampato dallo strumento.

## Stato di partenza
- from-docker --observe esiste nella PR nanovms/ops#1802 (fork:
  AngeloRubens/ops). Limiti noti: richiede Docker, usa strace da un'immagine
  Alpine con il loader musl, la scelta del programma è una stima, ciò che
  non viene eseguito durante l'osservazione non si vede.
- 12 server Java sono partiti sulla CI del fork, non in modo stabile
  (ActiveMQ, Helidon, WildFly a volte crollano nella JVM sotto emulazione).

## Quirk degli strumenti
- "gh pr edit" non funziona (manca read:org): usare gh api -X PATCH.
- GitHub restituisce a volte 502 o "Internal Server Error": ritentare, dopo
  aver controllato se l'operazione era già andata a buon fine.
- Il fork CI su nanos usa rami check/** e gira su ubuntu-24.04.
