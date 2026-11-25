package forms

import (
	"slices"
	"testing"
)

func TestRequiredLabels(t *testing.T) {
	got, err := RequiredLabels([]byte(`
name: Work
body:
  - type: textarea
    attributes: { label: Outcome }
    validations: { required: true }
  - type: textarea
    attributes: { label: Notes }
  - type: dropdown
    attributes: { label: Size }
    validations: { required: true }
`))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Outcome", "Size"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMissing(t *testing.T) {
	body := "### Outcome\r\nShip it.\r\n\n" +
		"### Placeholder\n\n_No response_\n\n" +
		"### Empty list\n- \n* \n1. \n\n" +
		"### Boxes\n- [ ]\n- [x]\n- [X] \n\n" +
		"### Boxed answer\n- [ ] write the test\n\n" +
		"### Trailing space \nyes\n\n" +
		"### Blank\n\n   \n"
	required := []string{"Outcome", "Placeholder", "Empty list", "Boxes", "Boxed answer", "Trailing space", "Blank", "Absent"}
	got := Missing(body, required)
	want := []string{"Placeholder", "Empty list", "Boxes", "Blank", "Absent"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMissingIgnoresTextBeforeFirstHeading(t *testing.T) {
	if got := Missing("Outcome: done\n### Outcome\n", []string{"Outcome"}); !slices.Equal(got, []string{"Outcome"}) {
		t.Fatalf("got %v", got)
	}
}
