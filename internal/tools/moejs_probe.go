package tools

// Keep the JavaScript engine linked into the production binary so `just
// performance` measures what shipping the script tool will actually cost. The
// script tool itself lands in a later step; this reference exists only so the
// size budget raised in the same change is checked against the real link
// instead of an estimate.
//
// Delete this file once the script tool imports the engine for real.
import (
	_ "github.com/MoeForge/moejs"
)
