> **Status note (August 2026):** This product specification retains historical
> phase-based intent. The authoritative source for current implementation and
> acceptance is the named [Capability Acceptance Registry](../process/acceptance_registry.md).
> In particular, its Phase 5 through Phase 7 headings map to `CAP-RESUME-REUSE`,
> `CAP-RESUME-COMPOSE`, and `CAP-RESUME-RENDER`.
Да. Я предлагаю сделать \*\*не одно “накопительное резюме”\*\*, а \*\*библиотеку базовых резюме под частые типы ролей\*\*.



Не так:



```text

Один огромный CV → каждый раз переделывать с нуля

```



А так:



```text

Career Vault → 4–6 approved base resume variants → быстрый reuse / modify / generate

```



Например для тебя:



```text

Base Resume 1 — SaaS / Software Engineer

Base Resume 2 — Implementation Engineer / Technical Consultant

Base Resume 3 — Technical Product / Business Systems Analyst

Base Resume 4 — Data / Analytics / BI Associate

Base Resume 5 — AI Automation / Workflow Automation Associate

Base Resume 6 — Support Engineer / Integration Specialist

```



Каждая версия будет \*\*один раз хорошо вручную проверена\*\*, а потом система сможет ее переиспользовать или слегка менять под похожие вакансии. Это экономит и деньги, и время, и снижает риск AI-галлюцинаций.



\---



\# Roadmap: Local-First Career Hub



\## Цель проекта



Создать дешевую локальную систему, которая:



```text

1\. Хранит твой опыт как reusable components.

2\. Хранит несколько approved base resume variants.

3\. Принимает JD вручную или через watcher.

4\. Оценивает вакансию.

5\. Решает: manual, assisted auto, archive, reject.

6\. Для assisted auto решает: reuse, modify, или generate from scratch.

7\. Рендерит резюме в твой approved Word template.

8\. Ведет application tracker.

```



Главный принцип:



```text

Generate from scratch only when reuse is not enough.

```



\---



\# Phase 0 — Подготовка шаблонов и структуры



\*\*Цель:\*\* зафиксировать основу, чтобы Codex не строил хаос.



\## Что сделать



1\. Создать репозиторий:



```text

career-hub/

```



2\. Создать базовую структуру:



```text

career-hub/

&#x20; career\_vault/

&#x20; resume\_versions/

&#x20; jobs/

&#x20; applications/

&#x20; templates/

&#x20; outputs/

&#x20; src/

&#x20; docs/

```



3\. Положить туда:



```text

templates/

&#x20; resume\_template.docx

&#x20; cover\_letter\_template.docx

```



4\. Создать файл проекта:



```text

docs/product\_spec.md

```



В нем описать:



```text

\- что система делает;

\- что система НЕ делает;

\- Method 1;

\- Method 2;

\- reuse logic;

\- forbidden: auto-apply, LinkedIn scraping, fake metrics.

```



\## Результат Phase 0



```text

Пустой, но правильно организованный проект.

```



\---



\# Phase 1 — Career Vault



\*\*Цель:\*\* разобрать твой большой CV/опыт на компоненты.



\## Структура



```text

career\_vault/

&#x20; profile.yaml

&#x20; education.yaml

&#x20; visa\_profile.yaml

&#x20; experience\_blocks.yaml

&#x20; project\_blocks.yaml

&#x20; skill\_blocks.yaml

&#x20; proof\_blocks.yaml

&#x20; style\_rules.yaml

```



\## Что хранить



\### `profile.yaml`



```yaml

name: "Example Candidate"

target\_location: "United States"

work\_authorization: "Example work authorization"

major: "Information Systems"

```



\### `experience\_blocks.yaml`



```yaml

\- id: example\_saas\_engineering

&#x20; company: Example SaaS Co

&#x20; role: Contractor Developer

&#x20; dates: "Apr 2026 – Present"

&#x20; category: "SaaS Engineering"

&#x20; technologies:

&#x20;   - React

&#x20;   - TypeScript

&#x20;   - Supabase

&#x20;   - PostgreSQL

&#x20;   - Deno Edge Functions

&#x20;   - JWT

&#x20;   - RLS

&#x20; proof\_points:

&#x20;   - "Built and stabilized multi-tenant SaaS features."

&#x20;   - "Worked on third-party integrations and analytics workflows."

&#x20;   - "Supported commerce, advertising, and analytics platform integrations."



```



\### `skill\_blocks.yaml`



```yaml

\- id: api\_integrations

&#x20; skill\_family: "API Integrations"

&#x20; related\_experience:

&#x20;   - example\_saas\_engineering

&#x20; keywords:

&#x20;   - REST APIs

&#x20;   - third-party integrations

&#x20;   - SaaS integrations

&#x20;   - data sync

```



\## Результат Phase 1



Система еще ничего не генерирует, но у нее уже есть \*\*источник правды\*\*.



Это важнее, чем UI.



\---



\# Phase 2 — Manual JD Intake

Current implementation note (July 2026): docs/guides/job_intake.md is the authoritative Phase 2 contract. Local deterministic code now extracts a conservative draft digest, including source-backed requirement groups and eligibility signals; a human still reviews unresolved fields. No AI API call is implemented.



\*\*Цель:\*\* ты вставляешь JD в файл, система создает job card.



\## Структура



```text

jobs/

&#x20; incoming/

&#x20;   red\_clay\_ai\_implementation.md

&#x20; normalized/

&#x20;   red\_clay\_ai\_implementation.yaml

```



\## Команда



```bash

python -m src.ingest\_job jobs/incoming/red\_clay\_ai\_implementation.md

```



\## Что система должна извлечь



```yaml

company: "Example Utilities Consulting"

title: "AI Implementation Analyst"

location: ""

remote\_policy: ""

seniority: "Early Career / Mid"

required\_skills:

&#x20; - SQL

&#x20; - implementation

&#x20; - client support

&#x20; - workflow analysis

preferred\_skills:

&#x20; - AI tools

&#x20; - utilities experience

visa\_signals:

&#x20; citizenship\_required: null

&#x20; clearance\_required: null

&#x20; sponsorship\_mentioned: null

&#x20; no\_visa\_or\_opt: null

raw\_jd\_path: "jobs/incoming/red\_clay\_ai\_implementation.md"

```



\## Результат Phase 2



Ты можешь вставлять вакансии вручную, а система будет приводить их к одному формату.



\---



\# Phase 3 — Scoring Engine

Current implementation note (July 2026): docs/guides/job_review.md is the authoritative Phase 3 contract. The current engine separates non-enforcing eligibility blockers from requirement gaps, uses exact evidence matching, and assigns review lanes without a numeric score or AI inference. The numeric model and blocker terminology below are retained only as an earlier product idea.



\*\*Цель:\*\* система решает, стоит ли тратить время.



\## Output lanes



```text

PRIORITY\_MANUAL

MANUAL\_REVIEW

ASSISTED\_AUTO\_DRAFT

ARCHIVE

REJECT

```



\## Логика



\### Hard blockers



```text

Reject if:

\- U.S. citizenship required

\- active security clearance required

\- explicit no OPT / no visa candidates

\- impossible location

\- seniority gap too large

\- core required skill completely missing

```



\### Score



```text

title\_fit: 0–20

technical\_fit: 0–25

domain\_fit: 0–15

implementation\_fit: 0–15

visa\_risk: 0–10

stem\_opt\_fit: 0–10

referral\_value: 0–5

```



\### Lane decision



```text

90–100: PRIORITY\_MANUAL

75–89: MANUAL\_REVIEW

60–74: ASSISTED\_AUTO\_DRAFT

40–59: ARCHIVE

0–39: REJECT

```



\## Результат Phase 3



На каждую вакансию система выдает:



```text

Lane: MANUAL\_REVIEW

Reason: Strong implementation and SaaS fit, but utilities domain experience is limited.

Biggest gap: Oracle Utilities / utility transformation background is not directly documented.

```



\---



\# Phase 4 — Base Resume Variants



\*\*Цель:\*\* создать 4–6 базовых резюме под частые типы ролей.



Это ключевая часть.



\## Структура



```text

resume\_versions/

&#x20; rv\_001\_saas\_software\_engineer/

&#x20;   resume.md

&#x20;   resume.docx

&#x20;   resume.pdf

&#x20;   metadata.yaml



&#x20; rv\_002\_implementation\_engineer/

&#x20;   resume.md

&#x20;   resume.docx

&#x20;   resume.pdf

&#x20;   metadata.yaml



&#x20; rv\_003\_technical\_product\_analyst/

&#x20;   resume.md

&#x20;   resume.docx

&#x20;   resume.pdf

&#x20;   metadata.yaml

```



\## Базовые варианты для тебя



\### 1. SaaS / Software Engineer



Для ролей:



```text

Software Engineer

Frontend Engineer

Full-Stack Developer

SaaS Engineer

Junior Software Engineer

```



Фокус:



```text

React, TypeScript, Supabase, PostgreSQL, Deno Edge Functions, API integrations, multi-tenant SaaS.

```



\---



\### 2. Implementation Engineer / Technical Consultant



Для ролей:



```text

Implementation Engineer

Technical Consultant

Solutions Associate

Integration Specialist

Client Implementation Specialist

```



Фокус:



```text

workflow analysis, client-facing implementation, integrations, troubleshooting, documentation, SaaS configuration.

```



\---



\### 3. Technical Product / Business Systems Analyst



Для ролей:



```text

Technical Product Analyst

Business Systems Analyst

Product Operations Analyst

Implementation Analyst

AI Innovation Associate

```



Фокус:



```text

requirements, workflows, product analysis, internal tools, technical documentation, stakeholder support.

```



\---



\### 4. Data / Analytics / BI Associate



Для ролей:



```text

Data Analyst

BI Analyst

Analytics Associate

Operations Analyst

Revenue Analyst

```



Фокус:



```text

PostgreSQL, data reconciliation, P\&L logic, marketplace reporting parity, ads metrics, reporting, dashboards.

```



\---



\### 5. AI Automation / Workflow Automation Associate



Для ролей:



```text

AI Automation Specialist

AI Operations Associate

Workflow Automation Specialist

AI Implementation Associate

```



Фокус:



```text

LLM tooling, Codex/Claude workflows, automation, product support, internal AI tools, process improvement.

```



\---



\### 6. Support Engineer / Technical Support Specialist



Для ролей:



```text

Support Engineer

Technical Support Engineer

Application Support Analyst

Customer Success Engineer

```



Фокус:



```text

debugging, integrations, customer issues, production support, data investigations, technical communication.

```



\## Результат Phase 4



У тебя появляется \*\*resume library\*\*, а не один документ.



Это резко ускоряет подачу.



\---



\# Phase 5 — Reuse Decision Engine



\*\*Цель:\*\* для Method 2 система решает, что делать с прошлым резюме.



\## Возможные решения



```text

REUSE\_PREVIOUS

MODIFY\_PREVIOUS

GENERATE\_FROM\_SCRATCH

```



\## Логика



```text

If similarity >= 85 and previous resume is manual-approved:

&#x20;   REUSE\_PREVIOUS



If similarity 65–84:

&#x20;   MODIFY\_PREVIOUS



If similarity < 65:

&#x20;   GENERATE\_FROM\_SCRATCH or ARCHIVE

```



\## Что сравнивать



```text

\- role family

\- seniority

\- required skills

\- preferred skills

\- domain

\- keywords

\- previous resume outcome

\- manual approval status

```



\## Пример output



```text

Lane: ASSISTED\_AUTO\_DRAFT

Reuse decision: MODIFY\_PREVIOUS

Closest resume: rv\_002\_implementation\_engineer

Reason:

\- Same role family.

\- 74% keyword overlap.

\- Previous resume already covers API integrations and SaaS implementation.

\- Needs stronger SQL and reporting emphasis for this JD.

```



\## Результат Phase 5



Система больше не пишет всё с нуля. Она сначала ищет ближайшую approved версию.



\---



\# Phase 6 — Resume Composer



\*\*Цель:\*\* собрать резюме под JD.



\## Для `REUSE\_PREVIOUS`



Система делает:



```text

\- берет previous resume;

\- делает keyword sanity check;

\- создает application notes;

\- не переписывает bullets без причины.

```



\## Для `MODIFY\_PREVIOUS`



Система делает:



```text

\- берет previous resume;

\- сравнивает с JD;

\- меняет summary;

\- меняет skills ordering;

\- заменяет 1–3 bullets;

\- сохраняет verified bullets.

```



\## Для `GENERATE\_FROM\_SCRATCH`



Система делает:



```text

\- выбирает evidence blocks из Career Vault;

\- собирает новое targeted resume;

\- помечает как not manually approved;

\- требует review.

```



\## Результат Phase 6



На выходе:



```text

outputs/company\_role\_date/

&#x20; resume\_draft.md

&#x20; job\_analysis.md

&#x20; selected\_evidence.md

&#x20; reuse\_decision.md

```



\---



\# Phase 7 — DOCX/PDF Renderer



\*\*Цель:\*\* решить твою главную боль с Word.



\## Подход



Не просить AI делать Word.



Вместо этого:



```text

approved resume\_template.docx

\+

placeholders

\+

generated content

=

clean resume.docx

```



\## Пример placeholders



```text

{{name}}

{{summary}}

{{skills\_line\_1}}

{{job\_1\_role}}

{{job\_1\_company}}

{{job\_1\_date}}

{{job\_1\_bullet\_1}}

{{job\_1\_bullet\_2}}

```



\## Output



```text

outputs/company\_role\_date/

&#x20; resume.docx

&#x20; resume.pdf

&#x20; cover\_letter.docx

&#x20; cover\_letter.pdf

```



\## Результат Phase 7



Ты больше не вставляешь всё вручную в Word. Система вставляет в твой approved template.



\---



\# Phase 8 — Application Tracker



\*\*Цель:\*\* вести воронку.



\## Статусы



```text

FOUND

ANALYZED

ARCHIVED

REJECTED

AUTO\_DRAFT\_CREATED

MANUAL\_REVIEW\_NEEDED

READY\_TO\_APPLY

APPLIED

FOLLOW\_UP\_NEEDED

INTERVIEW

REJECTED\_BY\_COMPANY

OFFER

CLOSED

```



\## Поля



```text

company

role

source

date\_found

date\_applied

lane

reuse\_decision

resume\_version\_used

cover\_letter\_version

status

next\_follow\_up\_date

outcome

notes

```



\## Результат Phase 8



У тебя появляется мини-CRM для job search.



\---



\# Phase 9 — Local Job Watcher



\*\*Цель:\*\* дешево находить новые вакансии.



\## Не сразу. После того как работает manual flow.



\## Источники



```text

companies.yaml

email alerts

company career pages

Greenhouse / Lever / Workday links

manual saved links

```



\## Логика



```text

cron / Windows Task Scheduler

↓

check pages every 6–12 hours

↓

find new job URLs

↓

save raw JD

↓

run cheap scoring

↓

only good jobs go to AI generation

```



\## Результат Phase 9



Watcher помогает находить вакансии, но не тратит деньги на каждую страницу.



\---



\# Phase 10 — Local Dashboard



\*\*Цель:\*\* удобный интерфейс, но без дорогого хостинга.



\## Варианты



```text

Streamlit local

simple HTML report

SQLite browser

CLI first, dashboard later

```



\## Что показывать



```text

\- job pipeline

\- pending review

\- applied

\- archived

\- resume variants

\- follow-ups

\- stats by role family

```



\## Результат Phase 10



Удобная панель, но всё еще почти бесплатно.



\---



\# Итоговый timeline



\## Week 1 — Foundation



```text

\- repo structure

\- career vault schema

\- manual JD input

\- basic job card generation

```



\## Week 2 — Scoring + lanes



```text

\- hard blockers

\- scoring engine

\- lane decision

\- archive/reject logic

```



\## Week 3 — Base resume variants



```text

\- create 4–6 base resume variants

\- manually approve them

\- metadata for each version

```



\## Week 4 — Reuse engine



```text

\- find closest resume version

\- reuse / modify / generate decision

\- output reuse reasoning

```



\## Week 5 — Resume composer



```text

\- generate resume markdown

\- modify previous resume

\- selected evidence reports

```



\## Week 6 — DOCX renderer



```text

\- add docxtpl

\- map approved Word template

\- export resume.docx

\- optional PDF export

```



\## Week 7 — Tracker



```text

\- SQLite tracker

\- statuses

\- application history

\- resume version history

```



\## Week 8 — Cron watcher



```text

\- companies.yaml

\- local scheduled watcher

\- new job detection

\- cheap scoring only

```



\---



\# MVP scope



Самый первый MVP должен быть вот таким:



```text

Manual JD input

\+

Career Vault

\+

Scoring Engine

\+

Base Resume Variant Selection

\+

Reuse / Modify / Generate Decision

\+

Markdown Resume Draft

```



Не надо начинать с:



```text

\- scraper

\- dashboard

\- cloud hosting

\- auto-apply

\- LinkedIn automation

\- perfect PDF export

```



\---



\# Codex prompt для первого этапа



```text

Create a local-first Python MVP for a personal Career Hub.



The system should help manage job applications and generate tailored resume drafts using reusable career evidence and resume version memory.



Do not build scrapers yet.

Do not build auto-apply.

Do not use Google Apps Script.

Do not build a web dashboard yet.

Do not invent resume facts.



Core requirements:



1\. Folder structure:

\- career\_vault/

\- resume\_versions/

\- jobs/incoming/

\- jobs/normalized/

\- applications/

\- outputs/

\- templates/

\- src/

\- docs/



2\. Career Vault:

Use YAML files to store:

\- profile

\- education

\- visa profile

\- experience blocks

\- project blocks

\- skill blocks

\- proof blocks

\- style rules



3\. Job Intake:

Accept a raw job description as a Markdown file in jobs/incoming/.

Normalize it into a YAML job card with:

\- company

\- title

\- location

\- remote policy

\- seniority

\- required skills

\- preferred skills

\- visa signals

\- clearance/citizenship signals

\- STEM OPT relevance

\- raw JD path



4\. Scoring Engine:

Classify each job into:

\- PRIORITY\_MANUAL

\- MANUAL\_REVIEW

\- ASSISTED\_AUTO\_DRAFT

\- ARCHIVE

\- REJECT



Detect hard blockers:

\- U.S. citizenship required

\- active security clearance required

\- explicit no OPT/no visa candidates

\- impossible location

\- seniority gap too large

\- missing core required skill



5\. Resume Version Library:

Store prior/base resume versions under resume\_versions/.

Each version must have:

\- resume.md

\- metadata.yaml



Metadata should include:

\- resume\_version\_id

\- role\_family

\- seniority

\- target\_keywords

\- skills\_covered

\- experience\_blocks\_used

\- manual\_reviewed

\- quality\_status

\- outcome



6\. Reuse Decision:

For ASSISTED\_AUTO\_DRAFT jobs, decide:

\- REUSE\_PREVIOUS

\- MODIFY\_PREVIOUS

\- GENERATE\_FROM\_SCRATCH



Use similarity logic based on:

\- role family

\- seniority

\- required skills overlap

\- preferred skills overlap

\- domain

\- manual approval status



7\. Outputs:

For each processed job, create an output folder with:

\- job\_analysis.md

\- selected\_evidence.md

\- reuse\_decision.md

\- resume\_draft.md

\- application\_notes.md



8\. Tracker:

Create a simple SQLite or CSV tracker with:

\- company

\- role

\- status

\- lane

\- reuse\_decision

\- resume\_version\_used

\- date\_found

\- date\_processed

\- notes



9\. CLI:

Provide commands such as:

\- process-job

\- score-job

\- list-applications

\- list-resume-versions



10\. Quality:

\- Keep dependencies minimal.

\- Use small functions.

\- Add README with setup and examples.

\- Include one sample career vault and one sample job description.

```



\---



\# Финальный roadmap в одну строку



```text

Сначала строим Career Vault и base resume library, потом scoring и reuse logic, потом DOCX renderer, потом tracker, и только после этого job watcher.

```
