# Calendar Dates

Career Vault editors now use native month calendars for employment, project,
education, graduation, and certification-completion dates. Application-status
updates retain their existing native day calendar because the ledger records a
specific event day.

## Stored Values

The editor keeps the existing display fields (`dates`, `graduation_date`, and
`completed`) for resume compatibility. When a calendar is used, it also writes
normalized metadata:

```yaml
dates: Feb 2026 - Present
date_range:
  start: 2026-02
  end: null
  ongoing: true
```

Schools may include `graduation_month`; certifications may include
`completed_month`. Both use `YYYY-MM`. The deterministic job reviewer prefers
`date_range` for duration calculations and falls back to existing human-readable
date text for records that have not yet been opened and saved in the editor.

Legacy date labels also populate the calendar when safe: ISO dates and
timestamps, full or abbreviated month names with a year, numeric month/year,
and common full-date formats. Separators may be a spaced hyphen, `to`, an en
dash, or an em dash. Dotted full dates use day-month-year; slash full dates use
the US month-day-year convention. A day or timestamp is normalized to its
calendar month because resume timelines are month-precision.

Standalone month tokens such as `Feb`, `February`, or `02` are accepted only
inside a range whose other endpoint supplies a year. The nearest chronological
year is inferred across a year boundary. A standalone month without any year
remains unchanged instead of being guessed.
