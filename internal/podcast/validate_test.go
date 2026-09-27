package podcast

import "testing"

func TestValidateCreate(t *testing.T) {
	if err := ValidateCreate(CreateInput{ID: "alice", Title: "Alice"}); err != nil {
		t.Fatal(err)
	}
	cases := []CreateInput{
		{ID: "", Title: "x"},
		{ID: "A", Title: "x"},
		{ID: "1alice", Title: "x"},
		{ID: "al", Title: ""},
		{ID: "v1", Title: "x"},
		{ID: "alice-", Title: "x"},
		{ID: "al--ice", Title: "x"},
	}
	for _, in := range cases {
		if err := ValidateCreate(in); err == nil {
			t.Fatalf("expected error for %+v", in)
		}
	}
}
