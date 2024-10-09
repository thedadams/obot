// Code generated by templ - DO NOT EDIT.

// templ: version: v0.2.778
package components

//lint:file-ignore SA4006 This context is only used if a nested component is present.

import "github.com/a-h/templ"
import templruntime "github.com/a-h/templ/runtime"

import (
	"github.com/otto8-ai/otto8/apiclient/types"
)

const (
	stepsInputBackgroundColor = " bg-gray-50 dark:bg-gray-700 "
	stepsInputColor           = "text-gray-900 dark:text-white"
	stepsLabelBackgroundColor = " bg-gray-200 dark:bg-gray-600 "
	stepsInputHighlightColor  = " focus-within:ring-blue-500 focus-within:border-blue-500 dark:focus-within:ring-blue-500 dark:focus-within:border-blue-500 "
	stepsBorderColor          = " border-gray-300 dark:border-gray-600 "
)

type stepsComponent struct {
	workflow     types.Workflow
	parentListID string
	stepList     []types.Step
	toolRefs     map[string]*types.ToolReference
}

func NewSteps(wf types.Workflow, parentListID string, stepList []types.Step, toolRefs map[string]*types.ToolReference) templ.Component {
	component := stepsComponent{
		workflow:     wf,
		parentListID: parentListID,
		stepList:     stepList,
		toolRefs:     toolRefs,
	}
	return component.main()
}

func (s stepsComponent) main() templ.Component {
	return templruntime.GeneratedTemplate(func(templ_7745c5c3_Input templruntime.GeneratedComponentInput) (templ_7745c5c3_Err error) {
		templ_7745c5c3_W, ctx := templ_7745c5c3_Input.Writer, templ_7745c5c3_Input.Context
		if templ_7745c5c3_CtxErr := ctx.Err(); templ_7745c5c3_CtxErr != nil {
			return templ_7745c5c3_CtxErr
		}
		templ_7745c5c3_Buffer, templ_7745c5c3_IsBuffer := templruntime.GetBuffer(templ_7745c5c3_W)
		if !templ_7745c5c3_IsBuffer {
			defer func() {
				templ_7745c5c3_BufErr := templruntime.ReleaseBuffer(templ_7745c5c3_Buffer)
				if templ_7745c5c3_Err == nil {
					templ_7745c5c3_Err = templ_7745c5c3_BufErr
				}
			}()
		}
		ctx = templ.InitializeContext(ctx)
		templ_7745c5c3_Var1 := templ.GetChildren(ctx)
		if templ_7745c5c3_Var1 == nil {
			templ_7745c5c3_Var1 = templ.NopComponent
		}
		ctx = templ.ClearChildren(ctx)
		var templ_7745c5c3_Var2 = []any{"flex flex-col gap-2 text-sm" + stepsInputColor}
		templ_7745c5c3_Err = templ.RenderCSSItems(ctx, templ_7745c5c3_Buffer, templ_7745c5c3_Var2...)
		if templ_7745c5c3_Err != nil {
			return templ_7745c5c3_Err
		}
		_, templ_7745c5c3_Err = templ_7745c5c3_Buffer.WriteString("<div class=\"")
		if templ_7745c5c3_Err != nil {
			return templ_7745c5c3_Err
		}
		var templ_7745c5c3_Var3 string
		templ_7745c5c3_Var3, templ_7745c5c3_Err = templ.JoinStringErrs(templ.CSSClasses(templ_7745c5c3_Var2).String())
		if templ_7745c5c3_Err != nil {
			return templ.Error{Err: templ_7745c5c3_Err, FileName: `components/steps.templ`, Line: 1, Col: 0}
		}
		_, templ_7745c5c3_Err = templ_7745c5c3_Buffer.WriteString(templ.EscapeString(templ_7745c5c3_Var3))
		if templ_7745c5c3_Err != nil {
			return templ_7745c5c3_Err
		}
		_, templ_7745c5c3_Err = templ_7745c5c3_Buffer.WriteString("\"")
		if templ_7745c5c3_Err != nil {
			return templ_7745c5c3_Err
		}
		if s.parentListID != "" {
			_, templ_7745c5c3_Err = templ_7745c5c3_Buffer.WriteString(" data-step-id=\"")
			if templ_7745c5c3_Err != nil {
				return templ_7745c5c3_Err
			}
			var templ_7745c5c3_Var4 string
			templ_7745c5c3_Var4, templ_7745c5c3_Err = templ.JoinStringErrs(s.parentListID)
			if templ_7745c5c3_Err != nil {
				return templ.Error{Err: templ_7745c5c3_Err, FileName: `components/steps.templ`, Line: 35, Col: 41}
			}
			_, templ_7745c5c3_Err = templ_7745c5c3_Buffer.WriteString(templ.EscapeString(templ_7745c5c3_Var4))
			if templ_7745c5c3_Err != nil {
				return templ_7745c5c3_Err
			}
			_, templ_7745c5c3_Err = templ_7745c5c3_Buffer.WriteString("\" data-workflow-id=\"")
			if templ_7745c5c3_Err != nil {
				return templ_7745c5c3_Err
			}
			var templ_7745c5c3_Var5 string
			templ_7745c5c3_Var5, templ_7745c5c3_Err = templ.JoinStringErrs(s.parentListID)
			if templ_7745c5c3_Err != nil {
				return templ.Error{Err: templ_7745c5c3_Err, FileName: `components/steps.templ`, Line: 36, Col: 45}
			}
			_, templ_7745c5c3_Err = templ_7745c5c3_Buffer.WriteString(templ.EscapeString(templ_7745c5c3_Var5))
			if templ_7745c5c3_Err != nil {
				return templ_7745c5c3_Err
			}
			_, templ_7745c5c3_Err = templ_7745c5c3_Buffer.WriteString("\"")
			if templ_7745c5c3_Err != nil {
				return templ_7745c5c3_Err
			}
		}
		_, templ_7745c5c3_Err = templ_7745c5c3_Buffer.WriteString(">")
		if templ_7745c5c3_Err != nil {
			return templ_7745c5c3_Err
		}
		for _, currentStep := range s.stepList {
			templ_7745c5c3_Err = NewStep(s.workflow, s.parentListID, currentStep, s.toolRefs).Render(ctx, templ_7745c5c3_Buffer)
			if templ_7745c5c3_Err != nil {
				return templ_7745c5c3_Err
			}
		}
		_, templ_7745c5c3_Err = templ_7745c5c3_Buffer.WriteString("<button class=\"w-full rounded-lg p-2 border-2 border-blue-600 hover:border-blue-500 dark:text-white\" hx-get=\"")
		if templ_7745c5c3_Err != nil {
			return templ_7745c5c3_Err
		}
		var templ_7745c5c3_Var6 string
		templ_7745c5c3_Var6, templ_7745c5c3_Err = templ.JoinStringErrs("/ui/workflows/" + s.workflow.ID + "/steps/new?parentID=" + s.parentListID)
		if templ_7745c5c3_Err != nil {
			return templ.Error{Err: templ_7745c5c3_Err, FileName: `components/steps.templ`, Line: 43, Col: 91}
		}
		_, templ_7745c5c3_Err = templ_7745c5c3_Buffer.WriteString(templ.EscapeString(templ_7745c5c3_Var6))
		if templ_7745c5c3_Err != nil {
			return templ_7745c5c3_Err
		}
		_, templ_7745c5c3_Err = templ_7745c5c3_Buffer.WriteString("\" hx-target=\"#modal-new-step\">Add Step</button><script>\n            (() => {\n                const el = $(document.currentScript).closest(\"div\")[0];\n                htmx.onLoad(function(content) {\n                    new Sortable(el, {\n                        animation: 150,\n                        ghostClass: 'blue-background-class',\n                        onEnd: function(evt) {\n                            const stepID = $(el).data(\"step-id\");\n                            const workflowID = $(el).data(\"workflow-id\");\n                            const newIndex = evt.newIndex;\n                            const oldIndex = evt.oldIndex;\n                            const url = \"/ui/workflows/\"+workflowID+\"/steps/\"+stepID+\"/move?newIndex=\"+newIndex+\"&oldIndex=\"+oldIndex;\n                            htmx.ajax(\"put\", url);\n                        },\n                    });\n                })\n            })()\n        </script></div>")
		if templ_7745c5c3_Err != nil {
			return templ_7745c5c3_Err
		}
		return templ_7745c5c3_Err
	})
}

var _ = templruntime.GeneratedTemplate
