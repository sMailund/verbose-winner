package terraform

import (
	"fmt"
	"log"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/hashicorp/terraform/internal/addrs"
	"github.com/hashicorp/terraform/internal/configs"
	"github.com/hashicorp/terraform/internal/instances"
	"github.com/hashicorp/terraform/internal/lang"
	"github.com/hashicorp/terraform/internal/tfdiags"
)

type checkType int

const (
	checkInvalid               checkType = 0
	checkResourcePrecondition  checkType = 1
	checkResourcePostcondition checkType = 2
	checkOutputPrecondition    checkType = 3
)

func (c checkType) FailureSummary() string {
	switch c {
	case checkResourcePrecondition:
		return "Resource precondition failed"
	case checkResourcePostcondition:
		return "Resource postcondition failed"
	case checkOutputPrecondition:
		return "Module output value precondition failed"
	default:
		// This should not happen
		return "Failed condition for invalid check type"
	}
}

// evalCheckRules ensures that all of the given check rules pass against
// the given HCL evaluation context.
//
// If any check rules produce an unknown result then they will be silently
// ignored on the assumption that the same checks will be run again later
// with fewer unknown values in the EvalContext.
//
// If any of the rules do not pass, the returned diagnostics will contain
// errors. Otherwise, it will either be empty or contain only warnings.
func evalCheckRules(typ checkType, rules []*configs.CheckRule, ctx EvalContext, self addrs.Referenceable, keyData instances.RepetitionData) (diags tfdiags.Diagnostics) {
	if len(rules) == 0 {
		// Nothing to do
		return nil
	}

	for _, rule := range rules {
		const errInvalidCondition = "Invalid condition result"
		var ruleDiags tfdiags.Diagnostics

		refs, moreDiags := lang.ReferencesInExpr(rule.Condition)
		ruleDiags = ruleDiags.Append(moreDiags)
		scope := ctx.EvaluationScope(self, keyData)
		hclCtx, moreDiags := scope.EvalContext(refs)
		ruleDiags = ruleDiags.Append(moreDiags)

		result, hclDiags := rule.Condition.Value(hclCtx)
		ruleDiags = ruleDiags.Append(hclDiags)
		diags = diags.Append(ruleDiags)

		if ruleDiags.HasErrors() {
			log.Printf("[TRACE] evalCheckRules: %s: %s", typ.FailureSummary(), ruleDiags.Err().Error())
		}

		if !result.IsKnown() {
			continue // We'll wait until we've learned more, then.
		}
		if result.IsNull() {
			diags = diags.Append(&hcl.Diagnostic{
				Severity:    hcl.DiagError,
				Summary:     errInvalidCondition,
				Detail:      "Condition expression must return either true or false, not null.",
				Subject:     rule.Condition.Range().Ptr(),
				Expression:  rule.Condition,
				EvalContext: hclCtx,
			})
			continue
		}
		var err error
		result, err = convert.Convert(result, cty.Bool)
		if err != nil {
			diags = diags.Append(&hcl.Diagnostic{
				Severity:    hcl.DiagError,
				Summary:     errInvalidCondition,
				Detail:      fmt.Sprintf("Invalid validation condition result value: %s.", tfdiags.FormatError(err)),
				Subject:     rule.Condition.Range().Ptr(),
				Expression:  rule.Condition,
				EvalContext: hclCtx,
			})
			continue
		}

		if result.False() {
			diags = diags.Append(&hcl.Diagnostic{
				Severity:    hcl.DiagError,
				Summary:     typ.FailureSummary(),
				Detail:      rule.ErrorMessage,
				Subject:     rule.Condition.Range().Ptr(),
				Expression:  rule.Condition,
				EvalContext: hclCtx,
			})
		}
	}

	return diags
}
