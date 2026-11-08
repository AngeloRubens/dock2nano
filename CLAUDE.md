# dock2nano (nome di lavoro, costante ToolName in cmd/name.go)

## Obiettivo
Migrazione a zero impatto di CI/CD basate su Docker e docker compose verso nanos.
L'utente non cambia il suo Dockerfile né il suo docker-compose.yml. Lo strumento
funziona anche con un Dockerfile solo, senza compose (un servizio). Lo strumento
li legge, costruisce i pacchetti e le immagini nanos, e genera un compose.yaml
che "ops compose" esegue. Ciò che non si converte viene stampato, mai scartato
in silenzio. Si punta allo zero impatto, non lo si garantisce.

## Confini
- Strumento esterno a ops: non si modifica il core di ops. Si chiama ops
  (ops compose, la creazione di pacchetti e immagini) per ciò che ops già fa.
- Docker vive nello strumento, non in ops: Ian probabilmente non vuole
  dipendenze da Docker in ops. Costruire l'immagine, osservarla ed estrarne i
  file sono compiti dello strumento; a ops si passa una directory di pacchetto
  già pronta. L'osservazione sta dietro un'interfaccia (Observer), così il
  lavoro di nanovms/ops#1802 si può portare dentro lo strumento. Il Dockerfile
  si legge con un parser proprio (internal/dockerfile).
- Linguaggio: Go. Il nome del tool vive in una sola costante.
- Si chiama il binario "ops" (exec), senza importare i pacchetti Go di ops:
  nessuna dipendenza di ops entra in go.mod. Nella CI si installa ops prima.
  Decisione dell'utente.
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
- Il nome non e' definitivo: ToolName = "dock2nano" e' di lavoro. Il 2026-11-07
  si e' chiesto a Ian un suggerimento (commento su nanovms/ops#1802); ha
  risposto (16:24 UTC) che il nome e' una scelta dell'utente: ops e nanos non
  hanno una regola di nomi. Il repo e' AngeloRubens/dock2nano (pubblico dal 2026-11-08: privato i minuti
  di Actions finivano e la CI non partiva); se il
  nome cambia si rinomina il repo e si cambia la costante.
- Ian ha risposto sulla 1802 (2026-11-06): vuole uno strumento esterno, perche'
  creare un pacchetto e' un lavoro di cura, l'ecosistema ha idiomi diversi
  (python, java, go) e il problema non ha limite. Non ha risposto se
  "ops compose" debba leggere un docker-compose.yml: la strada scelta e'
  lo strumento a parte che genera un compose.yaml.
- Stato al 2026-11-08 (ramo feat/modules): parser compose e Dockerfile, wrapper
  docker, osservazione sotto strace (porting delle funzioni di nanovms/ops#1802,
  che resta aperta), generatore di pacchetti e compose.yaml, comando "convert"
  con rapporto di ciò che non si converte. ci e integration (Docker vero) sono
  verdi; e2e prova nginx + app Python con "ops compose up"; survey converte 15
  immagini; PetClinic su GlassFish + MySQL si converte ma non risponde su nanos.
- Controlli di qualità: ramo check/mutate-modules (ci/mutate_modules.py) annulla
  una regola per volta e ogni test deve fallire; i rami check/** non si fondono.
- Limiti noti di nanos emersi dalle prove: niente fork, tutto gira come root
  (mysqld rifiuta), "ops compose up" a volte va in panic se l'istanza dns
  è in ritardo (si ritenta), un servizio "db" si raggiunge come "db.service".
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
