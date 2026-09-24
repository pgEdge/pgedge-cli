package pgedgecli

import "testing"

// TestIndexStaysUnderBudget is what keeps the index's size honest.
//
// The design comment on internal/cli.NewLLMSCmd used to state the
// figure directly, and it drifted from 16 KB to 35 KB with nothing
// measuring it — while that size is the entire reason the index
// exists rather than a monolith. A number in a comment cannot
// hold a budget; this can.
//
// The failure message names the remedy in both directions, because
// both are legitimate: trim the index, or raise the budget on purpose.
func TestIndexStaysUnderBudget(t *testing.T) {
	got := len(LLMS)
	if got == 0 {
		t.Fatal("the embedded index is empty; this test has lost its " +
			"subject and would pass against anything")
	}
	if got > IndexSizeBudget {
		t.Errorf("llms.txt is %d bytes, over the %d-byte budget by "+
			"%d. Either trim the index — it is the FIRST read every "+
			"agent makes, and it exists so that looking up one flag "+
			"does not cost every module's whole reference — or raise "+
			"IndexSizeBudget deliberately, in a commit that says why.",
			got, IndexSizeBudget, got-IndexSizeBudget)
	} else if got > IndexSizeBudget*9/10 {
		t.Logf("llms.txt is %d bytes, within 10%% of the %d-byte "+
			"budget — worth knowing before adding another section",
			got, IndexSizeBudget)
	}
}
