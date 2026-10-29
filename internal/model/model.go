// Package model holds the Go view of the discover report schema v1 (schema/discover-v1.schema.json).
//
// A field without `omitempty` is required by the schema; a field with it is optional. The other struct tags
// (enum, const, minLen, pattern, format, min, max, minItems, maxItems) carry the schema's value rules, so that
// Validate can enforce them without a JSON Schema library. A test keeps these types and the schema from drifting.
package model

// SchemaVersion is the only value `schema_version` may carry in a v1 report.
const SchemaVersion = "discover/v1"

// Report is the whole document discover writes.
type Report struct {
	SchemaVersion    string           `json:"schema_version" const:"discover/v1"`
	Tool             Tool             `json:"tool"`
	Scan             Scan             `json:"scan"`
	Repos            []Repo           `json:"repos" minItems:"1"`
	WorkspaceProfile WorkspaceProfile `json:"workspace_profile"`
	// Extensions is open to any content. A v1 reader ignores what it does not know, here as everywhere but in a finding.
	Extensions map[string]any `json:"extensions,omitempty"`
}

// Tool names the program that wrote the report.
type Tool struct {
	Name    string `json:"name" minLen:"1"`
	Version string `json:"version" minLen:"1"`
	Commit  string `json:"commit" minLen:"1"`
}

// Scan describes one run: when, over which targets, and the proof that nothing was written.
type Scan struct {
	Started  string   `json:"started" format:"date-time"`
	Finished string   `json:"finished" format:"date-time"`
	Targets  []Target `json:"targets" minItems:"1"`
	// ReadOnlyProofAllEqual summarises the per-repository proofs; they are the record.
	ReadOnlyProofAllEqual bool `json:"read_only_proof_all_equal"`
}

// Target is one path given to the scan. Mode says whether it is a git working tree or a plain directory.
type Target struct {
	Path string `json:"path" minLen:"1"`
	Mode string `json:"mode" enum:"git|dir"`
}

// Digest is a SHA-256 in 64 lowercase hex digits.
type Digest = string

// Tree measures one tree: what it holds and one digest of it.
type Tree struct {
	Files      int    `json:"files" min:"0"`
	Bytes      int    `json:"bytes" min:"0"`
	Links      int    `json:"links" min:"0"`
	Submodules int    `json:"submodules" min:"0"`
	Unreadable int    `json:"unreadable" min:"0"`
	Digest     Digest `json:"digest" pattern:"^[0-9a-f]{64}$"`
}

// GitState is what the scan knew of a git working tree. Branch and Head are empty when there is none.
type GitState struct {
	Branch     string   `json:"branch"`
	Detached   bool     `json:"detached"`
	Head       string   `json:"head"`
	Clean      bool     `json:"clean"`
	DirtyPaths []string `json:"dirty_paths" minLen:"1" pattern:"^(?:[^/\\\\]|[^/\\\\A-Za-z.][^/\\\\]+|[A-Za-z][^/\\\\:][^/\\\\]*|\\.[^/\\\\.][^/\\\\]*|\\.\\.[^/\\\\]+)(?:[/\\\\](?:[^/\\\\]|[^/\\\\.][^/\\\\]+|\\.[^/\\\\.][^/\\\\]*|\\.\\.[^/\\\\]+))*$"`
}

// ReadOnlyProof compares the tree digest before and after the scan of one repository.
type ReadOnlyProof struct {
	Status string `json:"status" enum:"EQUAL|NOT-EQUAL"`
	Before Digest `json:"before" pattern:"^[0-9a-f]{64}$"`
	After  Digest `json:"after" pattern:"^[0-9a-f]{64}$"`
}

// Source says where a claim comes from: a file, and a key or a line in it. At least one of Key and Line is set.
// File is relative to the repository root.
type Source struct {
	File string `json:"file" minLen:"1" pattern:"^(?:[^/\\\\]|[^/\\\\A-Za-z.][^/\\\\]+|[A-Za-z][^/\\\\:][^/\\\\]*|\\.[^/\\\\.][^/\\\\]*|\\.\\.[^/\\\\]+)(?:[/\\\\](?:[^/\\\\]|[^/\\\\.][^/\\\\]+|\\.[^/\\\\.][^/\\\\]*|\\.\\.[^/\\\\]+))*$"`
	Key  string `json:"key,omitempty" minLen:"1"`
	Line int    `json:"line,omitempty" min:"1"`
}

// Repo is the report of one repository. Its ten sections are fixed; an empty one is named in Absent.
type Repo struct {
	Name            string           `json:"name" minLen:"1"`
	Attestation     Attestation      `json:"attestation"`
	Inventory       Inventory        `json:"inventory"`
	Stack           []StackRow       `json:"stack"`
	Structure       Structure        `json:"structure"`
	EntryPoints     []EntryPoint     `json:"entry_points"`
	Commands        []Command        `json:"commands"`
	CI              []CI             `json:"ci"`
	Containers      []Container      `json:"containers"`
	Wiring          []Wiring         `json:"wiring"`
	Findings        []Finding        `json:"findings"`
	UI              []UIItem         `json:"ui"`
	Narrative       Narrative        `json:"narrative"`
	OpenQuestions   []OpenQuestion   `json:"open_questions"`
	Recommendations []Recommendation `json:"recommendations"`
	Absent          []Absent         `json:"absent"`
}

// Attestation echoes what the scan knew about the repository, the proof that it was left unchanged, and what it
// read. Root is the base name of the scanned directory, or its absolute path when the user asked to record it.
// Git is present exactly when Mode is "git"; Untracked only then, and optionally.
type Attestation struct {
	Root          string        `json:"root" minLen:"1"`
	Mode          string        `json:"mode" enum:"git|dir"`
	Git           *GitState     `json:"git,omitempty"`
	Tree          Tree          `json:"tree"`
	Untracked     *Tree         `json:"untracked,omitempty"`
	ReadOnlyProof ReadOnlyProof `json:"read_only_proof"`
	ReadManifest  ReadManifest  `json:"read_manifest"`
}

// ReadManifest lists every file the scan read.
type ReadManifest struct {
	Entries   []ManifestEntry `json:"entries"`
	FilesRead int             `json:"files_read" min:"0"`
	BytesRead int             `json:"bytes_read" min:"0"`
}

// ManifestEntry is one file read, with the digest of the bytes read.
type ManifestEntry struct {
	Path   string `json:"path" minLen:"1" pattern:"^(?:[^/\\\\]|[^/\\\\A-Za-z.][^/\\\\]+|[A-Za-z][^/\\\\:][^/\\\\]*|\\.[^/\\\\.][^/\\\\]*|\\.\\.[^/\\\\]+)(?:[/\\\\](?:[^/\\\\]|[^/\\\\.][^/\\\\]+|\\.[^/\\\\.][^/\\\\]*|\\.\\.[^/\\\\]+))*$"`
	SHA256 string `json:"sha256" pattern:"^[0-9a-f]{64}$"`
	Bytes  int    `json:"bytes" min:"0"`
}

// Inventory is section 1: the top level of the repository and its language mix.
type Inventory struct {
	TopLevel   []TopLevelEntry `json:"top_level"`
	Languages  []LanguageShare `json:"languages"`
	FilesTotal int             `json:"files_total" min:"0"`
	BytesTotal int             `json:"bytes_total" min:"0"`
}

// TopLevelEntry is one entry in the repository's root directory.
type TopLevelEntry struct {
	Name string `json:"name" minLen:"1"`
	Kind string `json:"kind" enum:"file|dir|symlink|other"`
}

// LanguageShare counts the files of one language.
type LanguageShare struct {
	Name  string `json:"name" minLen:"1"`
	Files int    `json:"files" min:"0"`
}

// StackRow is a row of section 2. Version is null, with VersionNote "not pinned", when no version is pinned.
type StackRow struct {
	Layer       string  `json:"layer" minLen:"1"`
	Technology  string  `json:"technology" minLen:"1"`
	Version     *string `json:"version"`
	VersionNote string  `json:"version_note,omitempty" minLen:"1"`
	Source      Source  `json:"source"`
}

// Structure is section 3.
type Structure struct {
	Patterns     []Observation `json:"patterns"`
	Naming       []Observation `json:"naming"`
	Architecture []Observation `json:"architecture"`
}

// Observation is a statement about the structure with at least one example.
type Observation struct {
	Text     string   `json:"text" minLen:"1"`
	Examples []Source `json:"examples" minItems:"1"`
}

// EntryPoint is a row of section 4.
type EntryPoint struct {
	Kind   string `json:"kind" enum:"main|cli|server|worker|library|script|other"`
	Name   string `json:"name" minLen:"1"`
	Source Source `json:"source"`
}

// Command is a row of section 5. It is cited, never executed: Executed is always false.
type Command struct {
	Kind     string `json:"kind" enum:"build|run|test|lint"`
	Text     string `json:"text" minLen:"1"`
	Source   Source `json:"source"`
	Executed bool   `json:"executed" const:"false"`
}

// CI is a continuous integration configuration found in the repository.
type CI struct {
	System string `json:"system" minLen:"1"`
	Source Source `json:"source"`
}

// Container is a container build or composition file found in the repository.
type Container struct {
	Kind   string `json:"kind" enum:"dockerfile|compose|kubernetes|other"`
	Image  string `json:"image,omitempty" minLen:"1"`
	Source Source `json:"source"`
}

// Wiring is a row of section 6: one claim about a dependency on, or by, another system.
type Wiring struct {
	Target   string `json:"target" minLen:"1"`
	Relation string `json:"relation" enum:"depends_on|depended_on_by|calls|called_by|shares"`
	Source   Source `json:"source"`
}

// Finding is a row of section 7. A secret finding carries SecretType and no Text; no field can hold the value.
type Finding struct {
	Kind       string `json:"kind" enum:"hot_spot|anti_pattern|secret|dead_code_candidate|todo|prompt_injection"`
	Severity   string `json:"severity" enum:"info|low|medium|high"`
	Source     Source `json:"source"`
	Text       string `json:"text,omitempty" minLen:"1"`
	SecretType string `json:"secret_type,omitempty" minLen:"1" pattern:"^[a-z0-9][a-z0-9-]{0,63}$"`
}

// UIItem is a row of section 8: a user interface string or screen, inventoried only.
type UIItem struct {
	Kind   string `json:"kind" enum:"string|screen"`
	Text   string `json:"text" minLen:"1"`
	Source Source `json:"source"`
}

// Narrative holds the model-written claims. Claims is empty when no model was used.
type Narrative struct {
	Model  string  `json:"model,omitempty" minLen:"1"`
	Claims []Claim `json:"claims"`
}

// Claim is one narrative statement; it needs at least one citation.
type Claim struct {
	Text      string   `json:"text" minLen:"1"`
	Citations []Source `json:"citations" minItems:"1"`
}

// OpenQuestion is a row of section 9.
type OpenQuestion struct {
	Topic    string   `json:"topic" minLen:"1"`
	Question string   `json:"question" minLen:"1"`
	Context  Source   `json:"context"`
	Options  []string `json:"options" minItems:"2" maxItems:"3" minLen:"1"`
}

// Recommendation is a row of section 10.
type Recommendation struct {
	Text      string   `json:"text" minLen:"1"`
	Citations []Source `json:"citations" minItems:"1"`
}

// Absent is the honest-absence line of an empty section.
type Absent struct {
	Section int    `json:"section" min:"1" max:"10"`
	Reason  string `json:"reason" minLen:"1"`
}

// WorkspaceProfile is what Studio Solo imports at setup.
type WorkspaceProfile struct {
	Repos []ProfileRepo `json:"repos" minItems:"1"`
}

// ProfileRepo is one repository in the workspace profile. RelativePath is relative to the scan root.
type ProfileRepo struct {
	Name            string          `json:"name" minLen:"1"`
	RelativePath    string          `json:"relative_path" pattern:"^(?:[^/\\\\]|[^/\\\\A-Za-z.][^/\\\\]+|[A-Za-z][^/\\\\:][^/\\\\]*|\\.[^/\\\\.][^/\\\\]*|\\.\\.[^/\\\\]+)(?:[/\\\\](?:[^/\\\\]|[^/\\\\.][^/\\\\]+|\\.[^/\\\\.][^/\\\\]*|\\.\\.[^/\\\\]+))*$"`
	VCS             string          `json:"vcs" enum:"git|none"`
	Languages       []string        `json:"languages" minLen:"1"`
	Commands        ProfileCommands `json:"commands"`
	ConventionFiles []string        `json:"convention_files" pattern:"^(?:[^/\\\\]|[^/\\\\A-Za-z.][^/\\\\]+|[A-Za-z][^/\\\\:][^/\\\\]*|\\.[^/\\\\.][^/\\\\]*|\\.\\.[^/\\\\]+)(?:[/\\\\](?:[^/\\\\]|[^/\\\\.][^/\\\\]+|\\.[^/\\\\.][^/\\\\]*|\\.\\.[^/\\\\]+))*$"`
	CI              []CI            `json:"ci"`
}

// ProfileCommands groups the cited commands by purpose.
type ProfileCommands struct {
	Build []ProfileCommand `json:"build"`
	Test  []ProfileCommand `json:"test"`
	Run   []ProfileCommand `json:"run"`
}

// ProfileCommand is a command with the place that cites it.
type ProfileCommand struct {
	Text   string `json:"text" minLen:"1"`
	Source Source `json:"source"`
}
