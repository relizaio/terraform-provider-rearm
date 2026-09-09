package provider

import "github.com/hashicorp/terraform-plugin-framework/diag"

// diagAdder is a tiny adapter so resource helpers can report errors without carrying the response type.
type diagAdder struct{ d *diag.Diagnostics }

func (a *diagAdder) err(summary, detail string) { a.d.AddError(summary, detail) }
