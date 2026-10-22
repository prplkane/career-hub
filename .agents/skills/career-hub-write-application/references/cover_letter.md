# Cover-Letter Material

Read this only for an editable or exportable cover-letter document.

1. Run `& $agent material-context --connection $connection --job-id <job_id> --kind cover_letter` once, only because an editable or exportable document was requested. Reserve `--pretty` for human inspection.
2. Preserve `editing_contract.code_owned`. Edit the allowed prose fields; change prefilled contact, company, role, date, or signature fields only for a factual reason.
3. Keep the canonical one-page structure: contact header, date, salutation, three direct body paragraphs, sign-off, name, and credential line. The paragraphs are confirmed motivation, verified evidence, and a concise close. Never invent motivation; leave the clear personalization placeholder/manual check unresolved until the applicant confirms it. Preserve two to four typed evidence items and their exact catalog `evidence_refs`; the renderer combines their claims into the single evidence paragraph.
4. Save the complete draft with `& $agent save-material --connection $connection --job-id <job_id> --kind cover_letter --expected-revision <revision> --stdin`. The receipt omits the editor; reuse its revision and reload context only when needed.
5. When a file is requested, run `& $agent render-material --connection $connection --job-id <job_id> --kind cover_letter --format <docx|pdf|txt>`. The export request is the commit boundary; do not add another approval step. The renderer owns formatting, filenames, and structural QA.

Do not use em dashes, en dashes, or the exact stock phrases listed in the parent skill.

Keep the Career Hub draft as the local source of truth. Do not hand-build Word files, edit DOCX/PDF internals, or create a Google Doc without an explicit request.
