package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
)

// diagAdder is a tiny adapter so resource helpers can report errors without carrying the response type.
type diagAdder struct{ d *diag.Diagnostics }

func (a *diagAdder) err(summary, detail string) { a.d.AddError(summary, detail) }

func (a *diagAdder) warn(summary, detail string) { a.d.AddWarning(summary, detail) }

func (a *diagAdder) attrErr(p path.Path, summary, detail string) {
	a.d.AddAttributeError(p, summary, detail)
}
