// career-hub-agent is a local HTTP client, never a second Data Home writer.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"careerhub.local/backend/internal/agent/agentcontext"
	"careerhub.local/backend/internal/agent/agentmaterials"
	"careerhub.local/backend/internal/jobs/jobdigest"
)

const help = `Career Hub Go agent preview

Commands: analyze, prepare, context, material-context, save-material, render-material, save-draft, registry-add-exact, record-submission
Required: --connection PATH (shown in Settings > Local agent connection); an optional
          --server http://127.0.0.1:PORT must match it; saved workflows require --job-id ID
Analyze: (--job-id ID | --stdin | --source-file PATH) [--registry-mode off|exact-auto]
         source-only metadata: --company NAME --title TITLE --source-url URL
Prepare: same source selection as Analyze; registers/starts preparation, never submits
         [--purpose assessment|application_text|resume_delta] [--registry-mode off|exact-auto]
         [--task-request-json JSON | --task-request-file PATH]
         source-only metadata additionally: --source-name NAME --posting-id ID
Options: --kind cover_letter|resume --pretty --timings
Context: --purpose assessment|application_text|resume_delta [--registry-mode off|exact-auto]
         [--task-request-json JSON | --task-request-file PATH]
Save: --expected-revision N [--approve] (--stdin | --payload-file PATH)
Text draft: save-draft (--stdin | --payload-file PATH); kind belongs in payload
Render: --format docx|pdf|txt
Exact vocabulary: --name TERM --primary-group-id ID [--source-line N] --confirm-exact
Submission record: --confirm-submitted [--resume-id ID] [--occurred-at ISO] [--note TEXT]

Start the explicitly enabled Go service first. No Python fallback, direct Data
Home writes, model calls, external submission or human fact-confirmation command is offered.
Export may require the user to review exact facts in the material editor.
`

func run(args []string, input io.Reader, output, diagnostic io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		_, _ = io.WriteString(output, help)
		return 0
	}
	started := time.Now()
	flags := flag.NewFlagSet("career-hub-agent", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	server := flags.String("server", "", "explicit local service origin")
	connection := flags.String("connection", "", "owner-only agent connection file shown in Settings")
	job := flags.String("job-id", "", "saved job identity")
	kind := flags.String("kind", "cover_letter", "material kind")
	format := flags.String("format", "", "export format")
	expected := flags.String("expected-revision", "", "optimistic saved revision")
	approve := flags.Bool("approve", false, "approve typed draft, not factual claims")
	stdin := flags.Bool("stdin", false, "read a JSON draft until EOF")
	file := flags.String("payload-file", "", "read one JSON payload file")
	name := flags.String("name", "", "reviewed exact JD technology")
	group := flags.String("primary-group-id", "", "active registry group")
	line := flags.String("source-line", "", "one-based exact source line")
	confirmExact := flags.Bool("confirm-exact", false, "confirm exact occurrence and completed semantic review")
	confirmSubmitted := flags.Bool("confirm-submitted", false, "confirm submission already occurred outside Career Hub")
	resumeID := flags.String("resume-id", "", "resume actually submitted")
	occurredAt := flags.String("occurred-at", "", "actual submission date or timestamp")
	note := flags.String("note", "", "submission history note")
	purpose := flags.String("purpose", "", "context purpose")
	registryMode := flags.String("registry-mode", "off", "vocabulary context policy, not a write")
	taskJSON := flags.String("task-request-json", "", "bounded typed context request")
	taskFile := flags.String("task-request-file", "", "one typed context request file")
	sourceFile := flags.String("source-file", "", "JD UTF-8 text file")
	company := flags.String("company", "", "source company")
	title := flags.String("title", "", "source job title")
	sourceURL := flags.String("source-url", "", "source posting URL")
	sourceName := flags.String("source-name", "", "source display filename, not a path")
	postingID := flags.String("posting-id", "", "source posting identity for deduplication")
	pretty := flags.Bool("pretty", false, "indent output")
	timings := flags.Bool("timings", false, "print elapsed time without private data")
	parseErr := flags.Parse(args[1:])
	fail := func(p *agentmaterials.Error) int {
		failure := map[string]any{"schema_version": 1, "contract_version": agentmaterials.ContractVersion, "result": "failed", "error": p.Error(), "error_code": p.Code, "operation_state": p.State, "retryable": false}
		if p.Stages != nil {
			failure["stages"] = p.Stages
		}
		if p.PreparationStages != nil {
			failure["preparation_stages"] = p.PreparationStages
		}
		if p.RunID != "" {
			failure["run_id"] = p.RunID
		}
		raw, _ := json.Marshal(failure)
		_, _ = output.Write(append(raw, '\n'))
		return 1
	}
	invalid := func() int { return fail(&agentmaterials.Error{Code: "AGENT_MATERIAL_ARGUMENTS_INVALID"}) }
	if parseErr == flag.ErrHelp {
		_, _ = io.WriteString(output, help)
		return 0
	}
	if parseErr != nil || flags.NArg() != 0 {
		return invalid()
	}
	if *timings {
		defer func() { _, _ = fmt.Fprintf(diagnostic, "{\"elapsed_ms\":%d}\n", time.Since(started).Milliseconds()) }()
	}
	var c *agentmaterials.Client
	var err *agentmaterials.Error
	if *connection != "" {
		c, err = agentmaterials.NewFromConnection(*connection, *server)
	} else {
		c, err = agentmaterials.New(*server)
	}
	if err != nil {
		return fail(err)
	}
	r := agentmaterials.Request{Command: args[0], JobID: *job, Kind: *kind, FileFormat: *format, Approve: *approve}
	invalidVocabularyFlag := false
	flags.Visit(func(f *flag.Flag) {
		if r.Command == "analyze" || r.Command == "prepare" {
			switch f.Name {
			case "server", "connection", "job-id", "pretty", "timings", "registry-mode", "stdin", "source-file", "company", "title", "source-url":
			case "purpose", "task-request-json", "task-request-file", "source-name", "posting-id":
				invalidVocabularyFlag = invalidVocabularyFlag || r.Command != "prepare"
			default:
				invalidVocabularyFlag = true
			}
			return
		}
		if f.Name == "source-file" || f.Name == "company" || f.Name == "title" || f.Name == "source-url" || f.Name == "source-name" || f.Name == "posting-id" {
			invalidVocabularyFlag = true
			return
		}
		contextFlag := f.Name == "purpose" || f.Name == "registry-mode" || f.Name == "task-request-json" || f.Name == "task-request-file"
		if r.Command == "context" {
			if !contextFlag && !connectionFlag(f.Name) && f.Name != "job-id" && f.Name != "pretty" && f.Name != "timings" {
				invalidVocabularyFlag = true
			}
			return
		} else if contextFlag {
			invalidVocabularyFlag = true
			return
		}
		vocabularyFlag := f.Name == "name" || f.Name == "primary-group-id" || f.Name == "source-line" || f.Name == "confirm-exact"
		submissionFlag := f.Name == "confirm-submitted" || f.Name == "resume-id" || f.Name == "occurred-at" || f.Name == "note"
		if r.Command == "record-submission" {
			if !submissionFlag && !connectionFlag(f.Name) && f.Name != "job-id" && f.Name != "pretty" && f.Name != "timings" {
				invalidVocabularyFlag = true
			}
		} else if submissionFlag {
			invalidVocabularyFlag = true
		} else if r.Command == "registry-add-exact" {
			if !vocabularyFlag && !connectionFlag(f.Name) && f.Name != "job-id" && f.Name != "pretty" && f.Name != "timings" {
				invalidVocabularyFlag = true
			}
		} else if vocabularyFlag {
			invalidVocabularyFlag = true
		}
	})
	if invalidVocabularyFlag {
		return invalid()
	}
	if r.Command == "analyze" || r.Command == "prepare" {
		r.Kind = ""
		if *registryMode != "off" && *registryMode != "exact-auto" {
			return invalid()
		}
		analysis := &agentmaterials.AnalyzeInput{RegistryMode: *registryMode}
		present := map[string]bool{}
		flags.Visit(func(f *flag.Flag) { present[f.Name] = true })
		if present["job-id"] {
			if *job == "" || present["stdin"] || present["source-file"] || present["company"] || present["title"] || present["source-url"] || present["source-name"] || present["posting-id"] {
				return invalid()
			}
		} else {
			if present["stdin"] == present["source-file"] || present["stdin"] && !*stdin {
				return invalid()
			}
			reader := input
			var closeSource func() error
			if present["source-file"] {
				f, e := os.Open(*sourceFile)
				if e != nil {
					return fail(&agentmaterials.Error{Code: "AGENT_CONTEXT_SOURCE_UNAVAILABLE"})
				}
				info, e := f.Stat()
				if e != nil || !info.Mode().IsRegular() || info.Size() > jobdigest.MaxSourceBytes {
					_ = f.Close()
					return invalid()
				}
				reader = f
				closeSource = f.Close
			}
			raw, e := io.ReadAll(io.LimitReader(reader, jobdigest.MaxSourceBytes+1))
			if closeSource != nil {
				if ce := closeSource(); e == nil {
					e = ce
				}
			}
			if e != nil || len(raw) > jobdigest.MaxSourceBytes {
				return fail(&agentmaterials.Error{Code: "AGENT_CONTEXT_SOURCE_UNAVAILABLE"})
			}
			text := strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\r", "\n")
			if present["source-file"] {
				text = strings.TrimPrefix(text, "\ufeff")
			}
			analysis.Source = &agentcontext.SourceRequest{Text: text, Company: *company, Title: *title, SourceURL: *sourceURL}
			if e := agentcontext.ValidateSourceRequest(*analysis.Source); e != nil {
				return fail(&agentmaterials.Error{Code: e.Code})
			}
		}
		if r.Command == "analyze" {
			r.Analysis = analysis
		} else {
			r.Preparation = &agentmaterials.PrepareInput{Source: analysis.Source, SourceName: *sourceName, PostingID: *postingID}
			if !present["purpose"] {
				*purpose = "assessment"
			}
		}
	}
	if r.Command == "context" || r.Command == "prepare" {
		r.Kind = ""
		if !agentcontext.ValidPurpose(*purpose) || *registryMode != "off" && *registryMode != "exact-auto" {
			return invalid()
		}
		intent := &agentmaterials.ContextInput{Purpose: *purpose, RegistryMode: *registryMode}
		jsonSet, fileSet := false, false
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "task-request-json" {
				jsonSet = true
			}
			if f.Name == "task-request-file" {
				fileSet = true
			}
		})
		if jsonSet && fileSet {
			return invalid()
		}
		if jsonSet {
			intent.TaskRequest = []byte(*taskJSON)
		}
		if fileSet {
			f, e := os.Open(*taskFile)
			if e != nil {
				return fail(&agentmaterials.Error{Code: "AGENT_CONTEXT_TASK_UNAVAILABLE"})
			}
			info, e := f.Stat()
			if e != nil || !info.Mode().IsRegular() || info.Size() > 4*agentcontext.MaxTaskJSONCharacters {
				_ = f.Close()
				return invalid()
			}
			raw, e := io.ReadAll(io.LimitReader(f, 4*agentcontext.MaxTaskJSONCharacters+1))
			closeErr := f.Close()
			if e != nil || closeErr != nil {
				return fail(&agentmaterials.Error{Code: "AGENT_CONTEXT_TASK_UNAVAILABLE"})
			}
			intent.TaskRequest = raw
		}
		if jsonSet || fileSet {
			if _, e := agentcontext.ParseTaskJSON(context.Background(), intent.TaskRequest, *purpose); e != nil {
				return fail(&agentmaterials.Error{Code: e.Code})
			}
		}
		if r.Command == "context" {
			r.Context = intent
		} else {
			r.Preparation.ContextInput = *intent
		}
	}
	if r.Command == "record-submission" {
		if !*confirmSubmitted {
			return invalid()
		}
		r.Kind = ""
		r.Submission = &agentmaterials.SubmissionInput{ConfirmedSubmitted: true, Note: *note}
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "resume-id" {
				r.Submission.ResumeID = resumeID
			}
			if f.Name == "occurred-at" {
				r.Submission.OccurredAt = occurredAt
			}
		})
	}
	if r.Command == "registry-add-exact" {
		r.Kind = ""
		r.Vocabulary = &agentmaterials.VocabularyInput{Name: *name, PrimaryGroupID: *group, ConfirmedExact: *confirmExact}
		if *name == "" || *group == "" || !*confirmExact {
			return invalid()
		}
		if *line != "" {
			n, err := strconv.Atoi(*line)
			if err != nil || n < 1 || n > 200000 {
				return invalid()
			}
			r.Vocabulary.SourceLine = &n
		} else {
			flags.Visit(func(f *flag.Flag) {
				if f.Name == "source-line" {
					invalidVocabularyFlag = true
				}
			})
			if invalidVocabularyFlag {
				return invalid()
			}
		}
	}
	if r.Command == "save-draft" {
		invalidFlag := false
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "kind" || f.Name == "expected-revision" || f.Name == "approve" || f.Name == "format" {
				invalidFlag = true
			}
		})
		if invalidFlag {
			return invalid()
		}
		r.Kind = ""
	}
	if r.Command == "save-material" || r.Command == "save-draft" {
		if r.Command == "save-material" {
			var ok bool
			r.ExpectedRevision, ok = agentmaterials.Revision(*expected)
			if !ok {
				return invalid()
			}
		}
		if *stdin == (*file != "") || *format != "" {
			return invalid()
		}
		reader := input
		if *file != "" {
			f, e := os.Open(*file)
			if e != nil {
				return fail(&agentmaterials.Error{Code: "AGENT_MATERIAL_PAYLOAD_UNAVAILABLE"})
			}
			defer f.Close()
			info, e := f.Stat()
			if e != nil || !info.Mode().IsRegular() || info.Size() > agentmaterials.MaxPayloadBytes {
				return invalid()
			}
			reader = f
		}
		payload, e := io.ReadAll(io.LimitReader(reader, agentmaterials.MaxPayloadBytes+1))
		if e != nil || len(payload) > agentmaterials.MaxPayloadBytes {
			return invalid()
		}
		r.Payload = payload
	} else if r.Command != "analyze" && r.Command != "prepare" && (*expected != "" || *stdin || *file != "" || *approve || r.Command == "material-context" && *format != "") {
		return invalid()
	}
	budget := 45 * time.Second
	if r.Command == "registry-add-exact" {
		budget = 50 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	raw, err := c.Run(ctx, r)
	if err != nil {
		return fail(err)
	}
	if *pretty {
		var formatted bytes.Buffer
		if json.Indent(&formatted, raw, "", "  ") != nil {
			return invalid()
		}
		raw = formatted.Bytes()
	}
	if _, e := output.Write(append(raw, '\n')); e != nil {
		return 1
	}
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

// connectionFlag reports the flags that select the local service; every
// command accepts them.
func connectionFlag(name string) bool { return name == "server" || name == "connection" }
