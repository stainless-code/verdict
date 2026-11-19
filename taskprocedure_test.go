package skillfs_test

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	skillfs "github.com/stainless-code/verdict"
)

// wantTasks is the task vocabulary written into the Procedure's mode router.
// It is a literal, not a copy of the list under test.
var wantTasks = []string{
	"address-reviews", "build", "ci", "fix-ci", "grade",
	"incremental-review", "review", "validate",
}

// TestTasks pins the served task names and their order.
func TestTasks(t *testing.T) {
	t.Parallel()
	if got := skillfs.Tasks(); !slices.Equal(got, wantTasks) {
		t.Fatalf("Tasks() = %v, want %v", got, wantTasks)
	}
}

type taskAssemblyCase struct {
	task  string
	files []string
}

// TestTaskProcedureAssemblies pins every body to the files its task names.
// The generic expectations use literal file lists read through skillfs.Read,
// never TaskProcedure or the assembly loop under test.
func TestTaskProcedureAssemblies(t *testing.T) {
	t.Parallel()
	for _, tc := range []taskAssemblyCase{
		{files: []string{"PROCEDURE.md", "prompts/system.md", "prompts/incremental-review.md"}, task: "incremental-review"},
		{files: []string{"PROCEDURE.md", "prompts/system.md", "prompts/fix-ci.md"}, task: "fix-ci"},
		{files: []string{"PROCEDURE.md", "prompts/system.md", "prompts/validate.md"}, task: "validate"},
		{files: []string{"PROCEDURE.md", "prompts/system.md", "prompts/build.md"}, task: "build"},
		{files: []string{"PROCEDURE.md", "prompts/system.md", "prompts/ci.md"}, task: "ci"},
		{files: []string{"PROCEDURE.md", "prompts/system.md", "prompts/grade.md"}, task: "grade"},
	} {
		t.Run(tc.task, func(t *testing.T) {
			t.Parallel()
			checkTaskAssembly(t, tc)
		})
	}
}

func checkTaskAssembly(t *testing.T, tc taskAssemblyCase) {
	t.Helper()
	var want []byte
	for _, name := range tc.files {
		part, err := skillfs.Read(name)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, part...)
	}
	got, err := skillfs.TaskProcedure(tc.task)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("TaskProcedure(%q) differs from the literal assembly of %v", tc.task, tc.files)
	}
}

type namedProcedureCase struct {
	want func() ([]byte, error)
	task string
}

// TestTaskProcedureReusesProcedureBodies pins the two named assemblies to the
// public functions the bundle already uses.
func TestTaskProcedureReusesProcedureBodies(t *testing.T) {
	t.Parallel()
	for _, tc := range []namedProcedureCase{
		{want: skillfs.ReviewProcedure, task: "review"},
		{want: skillfs.DrainProcedure, task: "address-reviews"},
	} {
		t.Run(tc.task, func(t *testing.T) {
			t.Parallel()
			checkNamedProcedureBody(t, tc)
		})
	}
}

func checkNamedProcedureBody(t *testing.T, tc namedProcedureCase) {
	t.Helper()
	want, err := tc.want()
	if err != nil {
		t.Fatal(err)
	}
	got, err := skillfs.TaskProcedure(tc.task)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("TaskProcedure(%q) differs from its named Procedure", tc.task)
	}
}

// TestTaskProcedureUnknownTask pins the typed refusal for a name outside the
// served vocabulary.
func TestTaskProcedureUnknownTask(t *testing.T) {
	t.Parallel()
	got, err := skillfs.TaskProcedure("nope")
	if got != nil {
		t.Fatalf("TaskProcedure(unknown) returned %d bytes", len(got))
	}
	unknown, ok := errors.AsType[*skillfs.UnknownTaskError](err)
	if !ok {
		t.Fatalf("TaskProcedure(unknown) error = %v, want *UnknownTaskError", err)
	}
	if want := "unknown task: nope"; unknown.Error() != want {
		t.Errorf("UnknownTaskError = %q, want %q", unknown.Error(), want)
	}
}
