// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"testing"
	"testing/fstest"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// invalidCase breaks the conformance project in one way and names the
// diagnostic that must follow.
type invalidCase struct {
	name string
	edit func(t *testing.T, m fstest.MapFS)
	opts func(t *testing.T, o *Options)
	code plxerr.Code
	file string
	ptr  string
}

// onPage edits the calculator page.
func onPage(f func(t *testing.T, doc map[string]any)) func(*testing.T, fstest.MapFS) {
	return func(t *testing.T, m fstest.MapFS) {
		edit(t, m, calculatorPage, func(doc map[string]any) { f(t, doc) })
	}
}

// graphDoc edits the calculate graph.
func graphDoc(f func(t *testing.T, doc map[string]any)) func(*testing.T, fstest.MapFS) {
	return func(t *testing.T, m fstest.MapFS) {
		edit(t, m, calculateGraph, func(doc map[string]any) { f(t, doc) })
	}
}

// setProp sets a prop of a node of the calculator page.
func setProp(node, prop, value string) func(*testing.T, fstest.MapFS) {
	return onPage(func(t *testing.T, doc map[string]any) {
		props := at(t, doc, node)["props"].(map[string]any)
		props[prop] = raw(t, value)
	})
}

// resolutionCases break identities and references.
func resolutionCases() []invalidCase {
	return []invalidCase{
		{name: "duplicate node ID", edit: onPage(func(t *testing.T, doc map[string]any) {
			at(t, doc, sliderNode)["id"] = at(t, doc, amountNode)["id"]
		}), code: plxerr.DuplicateID, file: calculatorPage, ptr: "/" + column},
		{name: "duplicate state name", edit: onPage(func(t *testing.T, doc map[string]any) {
			at(t, doc, "state/1")["name"] = "amount"
		}), code: plxerr.DuplicateKey, file: calculatorPage, ptr: "/state/1/name"},
		{name: "duplicate route name", edit: func(t *testing.T, m fstest.MapFS) {
			edit(t, m, resultPage, func(doc map[string]any) { doc["route"] = "loan-calculator" })
		}, code: plxerr.DuplicateRouteName, file: resultPage, ptr: "/route"},
		{name: "route name of a native route", edit: func(t *testing.T, m fstest.MapFS) {
			edit(t, m, resultPage, func(doc map[string]any) { doc["route"] = "account-overview" })
		}, code: plxerr.DuplicateRouteName, file: resultPage, ptr: "/route"},
		{
			name: "unknown translation key", edit: setProp(amountNode, "decoration", `{"labelText": {"$t": "01a0c450-6c00-7fff-8000-000000000001"}}`),
			code: plxerr.UnresolvedReference, file: calculatorPage, ptr: "/" + amountNode + "/props/decoration/labelText/$t",
		},
		{
			name: "unknown token", edit: setProp(column, "spacing", `{"$token": "space.huge"}`),
			code: plxerr.UnresolvedReference, file: calculatorPage, ptr: "/" + column + "/props/spacing/$token",
		},
		{name: "unknown graph", edit: onPage(func(t *testing.T, doc map[string]any) {
			at(t, doc, buttonNode+"/events/onPressed")["$graph"] = "01a0c450-6c00-7fff-8000-000000000002"
		}), code: plxerr.UnresolvedReference, file: calculatorPage, ptr: "/" + buttonNode + "/events/onPressed/$graph"},
		{name: "unknown entry page", edit: func(t *testing.T, m fstest.MapFS) {
			edit(t, m, pluginFile, func(doc map[string]any) { doc["entryPage"] = "01a0c450-6c00-7fff-8000-000000000003" })
		}, code: plxerr.UnresolvedReference, file: pluginFile, ptr: "/entryPage"},
		{name: "unknown entry route", edit: func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "app.json", func(doc map[string]any) { doc["entryRoute"] = "nowhere" })
		}, code: plxerr.UnknownRoute, file: "app.json", ptr: "/entryRoute"},
		{name: "unknown widget", edit: onPage(func(t *testing.T, doc map[string]any) {
			at(t, doc, sliderNode)["type"] = "Knob"
		}), code: plxerr.UnknownWidgetType, file: calculatorPage, ptr: "/" + sliderNode + "/type"},
		{name: "unknown component", edit: onPage(func(t *testing.T, doc map[string]any) {
			n := at(t, doc, sliderNode)
			delete(n, "type")
			n["component"] = raw(t, `{"id": "01a0c450-6c00-7fff-8000-000000000004", "version": 1}`)
		}), code: plxerr.UnresolvedReference, file: calculatorPage, ptr: "/" + sliderNode + "/component/id"},
		{name: "reserved type name", edit: func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "app.json", func(doc map[string]any) {
				doc["types"] = append(doc["types"].([]any), raw(t, `{"name": "PluxThing", "enum": ["a"]}`))
			})
		}, code: plxerr.DuplicateKey, file: "app.json", ptr: "/types/2/name"},
	}
}

// nodeCases break nodes against their descriptors.
func nodeCases() []invalidCase {
	return []invalidCase{
		{
			name: "unknown prop", edit: setProp(sliderNode, "colour", `"#FF0000"`),
			code: plxerr.UnknownProp, file: calculatorPage, ptr: "/" + sliderNode + "/props/colour",
		},
		{
			name: "prop type", edit: setProp(sliderNode, "divisions", `"many"`),
			code: plxerr.PropTypeMismatch, file: calculatorPage, ptr: "/" + sliderNode + "/props/divisions",
		},
		{
			name: "binding type", edit: setProp(sliderNode, "value", `{"$expr": "page.amount"}`),
			code: plxerr.PropTypeMismatch, file: calculatorPage, ptr: "/" + sliderNode + "/props/value/$expr",
		},
		{
			name: "token type", edit: setProp(sliderNode, "thumbColor", `{"$token": "space.md"}`),
			code: plxerr.PropTypeMismatch, file: calculatorPage, ptr: "/" + sliderNode + "/props/thumbColor/$token",
		},
		{
			name: "enum value", edit: setProp(column, "crossAxisAlignment", `"middle"`),
			code: plxerr.PropTypeMismatch, file: calculatorPage, ptr: "/" + column + "/props/crossAxisAlignment",
		},
		{
			name: "value type field", edit: setProp(amountNode, "decoration", `{"label": 3}`),
			code: plxerr.PropTypeMismatch, file: calculatorPage, ptr: "/" + amountNode + "/props/decoration",
		},
		{name: "missing required prop", edit: onPage(func(t *testing.T, doc map[string]any) {
			delete(at(t, doc, sliderNode)["props"].(map[string]any), "value")
		}), code: plxerr.MissingRequiredProp, file: calculatorPage, ptr: "/" + sliderNode},
		{
			name: "constraint", edit: setProp(sliderNode, "divisions", `0`),
			code: plxerr.ConstraintViolation, file: calculatorPage, ptr: "/" + sliderNode + "/props/divisions",
		},
		{name: "unknown event", edit: onPage(func(t *testing.T, doc map[string]any) {
			at(t, doc, sliderNode)["events"].(map[string]any)["onTap"] = raw(t, `{"steps": [{"id": "a", "action": "haptic"}]}`)
		}), code: plxerr.UnknownEvent, file: calculatorPage, ptr: "/" + sliderNode + "/events/onTap"},
		{name: "children on a leaf", edit: onPage(func(t *testing.T, doc map[string]any) {
			at(t, doc, sliderNode)["children"] = raw(t, `[{"id": "01a0c450-6c00-7fff-8000-000000000005", "type": "Text", "props": {"data": "x"}}]`)
		}), code: plxerr.InvalidChildren, file: calculatorPage, ptr: "/" + sliderNode + "/children"},
		{name: "unknown slot", edit: onPage(func(t *testing.T, doc map[string]any) {
			at(t, doc, "root/slots")["footer"] = raw(t, `{"id": "01a0c450-6c00-7fff-8000-000000000006", "type": "Text", "props": {"data": "x"}}`)
		}), code: plxerr.UnknownSlot, file: calculatorPage, ptr: "/root/slots/footer"},
		{name: "missing required slot", edit: onPage(func(t *testing.T, doc map[string]any) {
			col := at(t, doc, column)
			col["children"] = append(col["children"].([]any), raw(t, `{"id": "01a0c450-6c00-7fff-8000-000000000009", "type": "If", "props": {"condition": true}}`))
		}), code: plxerr.MissingRequiredSlot, file: calculatorPage, ptr: "/" + column + "/children/6"},
		{name: "list in a single slot", edit: onPage(func(t *testing.T, doc map[string]any) {
			slots := at(t, doc, buttonNode)["slots"].(map[string]any)
			slots["child"] = []any{slots["child"]}
		}), code: plxerr.InvalidChildren, file: calculatorPage, ptr: "/" + buttonNode + "/slots/child"},
		{name: "visible type", edit: onPage(func(t *testing.T, doc map[string]any) {
			at(t, doc, sliderNode)["visible"] = raw(t, `{"$expr": "page.months"}`)
		}), code: plxerr.PropTypeMismatch, file: calculatorPage, ptr: "/" + sliderNode + "/visible/$expr"},
		{name: "responsive prop", edit: onPage(func(t *testing.T, doc map[string]any) {
			at(t, doc, sliderNode)["responsive"] = raw(t, `{"medium": {"size": 3}}`)
		}), code: plxerr.UnknownProp, file: calculatorPage, ptr: "/" + sliderNode + "/responsive/medium/size"},
		{name: "concurrency", edit: onPage(func(t *testing.T, doc map[string]any) {
			at(t, doc, buttonNode+"/events/onPressed")["concurrency"] = "debounce:99999999"
		}), code: plxerr.InvalidFormat, file: calculatorPage, ptr: "/" + buttonNode + "/events/onPressed/concurrency"},
		{
			name: "PXL range", edit: setProp(sliderNode, "value", `{"$expr": "page.nothing"}`),
			code: plxerr.PXLUnknownField, file: calculatorPage, ptr: "/" + sliderNode + "/props/value/$expr",
		},
		{
			name: "string limit", edit: setProp(column+"/children/1", "data", `"xxxxxxxxxx"`),
			opts: tighten(limits.DocumentStringPropSize, 4),
			code: plxerr.LimitExceeded, file: calculatorPage, ptr: "/" + column + "/children/1/props/data",
		},
		{name: "redirect loop", edit: func(t *testing.T, m fstest.MapFS) {
			redirect(t, m, calculatorPage, calculatorRedirect)
			redirect(t, m, resultPage, resultRedirect)
		}, code: plxerr.RedirectLoop, file: calculatorPage, ptr: "/lifecycle/onEnter"},
		{name: "runtime too old", edit: func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "app.json", func(doc map[string]any) { doc["minRuntimeVersion"] = "0.0.9" })
		}, code: plxerr.RuntimeTooOld, file: calculatorPage, ptr: "/root/type"},
		{name: "features raised", edit: func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "app.json", func(doc map[string]any) {
				doc["minRuntimeVersion"] = "0.0.9"
				doc["requiredFeatures"] = "raise"
			})
		}, code: plxerr.RequiredFeaturesRaised, file: calculatorPage, ptr: "/root/type"},
	}
}

// tighten lowers a limit.
func tighten(k limits.Key, v int64) func(t *testing.T, o *Options) {
	return func(t *testing.T, o *Options) {
		t.Helper()
		var err error
		if o.Limits, err = o.Limits.Tighten(k, limits.ScopePlugin, v); err != nil {
			t.Fatal(err)
		}
	}
}

// declarationCases break defaults, mocks, types and graphs.
func declarationCases() []invalidCase {
	return []invalidCase{
		{
			name: "state default", edit: onPage(func(t *testing.T, doc map[string]any) { at(t, doc, "state/1")["default"] = "twelve" }),
			code: plxerr.ValueTypeMismatch, file: calculatorPage, ptr: "/state/1/default",
		},
		{
			name: "state without default", edit: onPage(func(t *testing.T, doc map[string]any) { delete(at(t, doc, "state/1"), "default") }),
			code: plxerr.InvalidStructure, file: calculatorPage, ptr: "/state/1",
		},
		{name: "computed type", edit: onPage(func(t *testing.T, doc map[string]any) {
			at(t, doc, "state/2/computed")["$expr"] = "string(page.months)"
		}), code: plxerr.ValueTypeMismatch, file: calculatorPage, ptr: "/state/2/computed/$expr"},
		{
			name: "unknown type", edit: onPage(func(t *testing.T, doc map[string]any) { at(t, doc, "state/1")["type"] = "Months" }),
			code: plxerr.UnknownType, file: calculatorPage, ptr: "/state/1/type",
		},
		{name: "registry type in a document", edit: onPage(func(t *testing.T, doc map[string]any) {
			at(t, doc, "state/1")["type"] = "EdgeInsets"
		}), code: plxerr.UnknownType, file: calculatorPage, ptr: "/state/1/type"},
		{
			name: "missing mock", edit: onPage(func(t *testing.T, doc map[string]any) { delete(at(t, doc, "params/0"), "mock") }),
			code: plxerr.MissingMock, file: calculatorPage, ptr: "/params/0",
		},
		{
			name: "mock type", edit: onPage(func(t *testing.T, doc map[string]any) { at(t, doc, "params/0")["mock"] = 7.0 }),
			code: plxerr.ValueTypeMismatch, file: calculatorPage, ptr: "/params/0/mock",
		},
		{name: "flag default", edit: func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "app.json", func(doc map[string]any) { at(t, doc, "flags/0")["default"] = "high" })
		}, code: plxerr.ValueTypeMismatch, file: "app.json", ptr: "/flags/0/default"},
		{name: "environment variable", edit: func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "app.json", func(doc map[string]any) { at(t, doc, "environments/0/values")["timeout"] = 3.0 })
		}, code: plxerr.UnresolvedReference, file: "app.json", ptr: "/environments/0/values/timeout"},
		{
			name: "unknown action", edit: graphDoc(func(t *testing.T, doc map[string]any) { at(t, doc, "steps/1")["action"] = "beep" }),
			code: plxerr.UnknownAction, file: calculateGraph, ptr: "/steps/1/action",
		},
		{name: "unknown input", edit: graphDoc(func(t *testing.T, doc map[string]any) {
			at(t, doc, "steps/1/input")["volume"] = 3.0
		}), code: plxerr.UnknownProp, file: calculateGraph, ptr: "/steps/1/input/volume"},
		{name: "missing input", edit: graphDoc(func(t *testing.T, doc map[string]any) {
			delete(at(t, doc, "steps/1/input"), "name")
		}), code: plxerr.MissingRequiredProp, file: calculateGraph, ptr: "/steps/1"},
		{
			name: "missing step", edit: graphDoc(func(t *testing.T, doc map[string]any) { at(t, doc, "steps/1")["next"] = "nowhere" }),
			code: plxerr.InvalidActionGraph, file: calculateGraph, ptr: "/steps/1/next",
		},
		{
			name: "cycle", edit: graphDoc(func(t *testing.T, doc map[string]any) { at(t, doc, "steps/2")["next"] = "store" }),
			code: plxerr.InvalidActionGraph, file: calculateGraph, ptr: "/steps",
		},
		{
			name: "unreachable step", edit: graphDoc(func(t *testing.T, doc map[string]any) { delete(at(t, doc, "steps/1"), "next") }),
			code: plxerr.InvalidActionGraph, file: calculateGraph, ptr: "/steps/2",
		},
		{name: "unknown branch", edit: graphDoc(func(t *testing.T, doc map[string]any) {
			at(t, doc, "steps/1")["branches"] = raw(t, `{"later": "open"}`)
		}), code: plxerr.InvalidActionGraph, file: calculateGraph, ptr: "/steps/1/branches/later"},
		{name: "unknown state path", edit: graphDoc(func(t *testing.T, doc map[string]any) {
			at(t, doc, "steps/0/input")["path"] = "plugin.nothing"
		}), code: plxerr.UnresolvedReference, file: calculateGraph, ptr: "/steps/0/input/path"},
		{
			name: "unknown route", edit: graphDoc(func(t *testing.T, doc map[string]any) { at(t, doc, "steps/2/input")["route"] = "nowhere" }),
			code: plxerr.UnknownRoute, file: calculateGraph, ptr: "/steps/2/input/route",
		},
		{name: "route parameter missing", edit: graphDoc(func(t *testing.T, doc map[string]any) {
			delete(at(t, doc, "steps/2/input/params"), "schedule")
		}), code: plxerr.RouteParameterMissing, file: calculateGraph, ptr: "/steps/2/input/params"},
		{name: "route parameter type", edit: graphDoc(func(t *testing.T, doc map[string]any) {
			at(t, doc, "steps/2/input/params")["schedule"] = "soon"
		}), code: plxerr.RouteParameterTypeInvalid, file: calculateGraph, ptr: "/steps/2/input/params/schedule"},
		{name: "unknown route parameter", edit: graphDoc(func(t *testing.T, doc map[string]any) {
			at(t, doc, "steps/2/input/params")["colour"] = "red"
		}), code: plxerr.UnknownRouteParameter, file: calculateGraph, ptr: "/steps/2/input/params/colour"},
		{name: "item outside a loop", edit: graphDoc(func(t *testing.T, doc map[string]any) {
			at(t, doc, "steps/1/input/props")["months"] = raw(t, `{"$expr": "item"}`)
		}), code: plxerr.PXLUnknownIdentifier, file: calculateGraph, ptr: "/steps/1/input/props/months/$expr"},
		{name: "event payload conflict", edit: onPage(func(t *testing.T, doc map[string]any) {
			at(t, doc, sliderNode)["events"].(map[string]any)["onChanged"] = raw(t, `{"$graph": "01a0c450-6c00-701b-8000-000000034335"}`)
		}), code: plxerr.InvalidActionGraph, file: calculatorPage, ptr: "/" + sliderNode + "/events/onChanged"},
	}
}

// policyCases break the policy tier.
func policyCases() []invalidCase {
	return []invalidCase{
		{name: "node budget", opts: tighten(limits.PageNodes, 5), code: plxerr.PageNodeBudget, file: calculatorPage, ptr: "/root"},
		{name: "depth budget", opts: tighten(limits.PageDepth, 3), code: plxerr.PageDepthBudget, file: calculatorPage, ptr: "/root"},
		{name: "build cost budget", opts: tighten(limits.PageBuildCost, 10), code: plxerr.PageBuildCostBudget, file: calculatorPage, ptr: "/root"},
		{name: "image budget", edit: func(t *testing.T, m fstest.MapFS) {
			edit(t, m, resultPage, func(doc map[string]any) {
				at(t, doc, "root/slots/body/slots")["child"] = raw(t, `{"id": "01a0c450-6c00-7fff-8000-000000000007", "type": "Image",
					"props": {"source": {"asset": {"$asset": "01a0c450-6c00-700d-8000-000000019223"}}, "semanticLabel": "Logo"}}`)
			})
		}, opts: tighten(limits.PageImageBytes, 1), code: plxerr.PageImageBudget, file: resultPage, ptr: "/root"},
		{
			name: "accessible name", edit: onPage(func(t *testing.T, doc map[string]any) { delete(at(t, doc, sliderNode), "semantics") }),
			code: plxerr.AccessibleNameMissing, file: calculatorPage, ptr: "/" + sliderNode,
		},
		{
			// The fake access key is assembled at run time so that secret
			// scanners do not flag the test's input.
			name: "secret", edit: setProp(column+"/children/1", "data", `"AKIA`+`ABCDEFGHIJKLMNOP"`),
			code: plxerr.SecretLikeValue, file: calculatorPage, ptr: "/" + column + "/children/1/props/data",
		},
		{
			name: "insecure URL", edit: setProp(column+"/children/1", "data", `"http://example.com"`),
			code: plxerr.InsecureURL, file: calculatorPage, ptr: "/" + column + "/children/1/props/data",
		},
		{name: "sensitive analytics", edit: func(t *testing.T, m fstest.MapFS) {
			edit(t, m, calculatorPage, func(doc map[string]any) { at(t, doc, "state/1")["sensitive"] = true })
		}, code: plxerr.SensitiveValueExposed, file: calculateGraph, ptr: "/steps/1/input/props"},
		{name: "executable asset", edit: func(t *testing.T, m fstest.MapFS) {
			m["assets/images/logo.png"] = &fstest.MapFile{Data: []byte("\x7fELF\x02\x01")}
		}, code: plxerr.ExecutableContent, file: "assets/index.json", ptr: "/assets/0/file"},
		{name: "SVG script", edit: func(t *testing.T, m fstest.MapFS) {
			m["assets/images/badge.svg"] = &fstest.MapFile{Data: []byte(`<svg onload="alert(1)"></svg>`)}
			edit(t, m, "assets/index.json", func(doc map[string]any) {
				doc["assets"] = append(doc["assets"].([]any), raw(t, `{"id": "01a0c450-6c00-7fff-8000-000000000008", "key": "badge",
					"file": "images/badge.svg", "mediaType": "image/svg+xml"}`))
			})
		}, code: plxerr.ExecutableContent, file: "assets/index.json", ptr: "/assets/1/file"},
	}
}

// Verifies: SCH-005, SCH-024, SCH-025, SCH-040, CMP-004, CMP-040, WGT-004,
// BND-011, SEC-054, SCH-012.
func TestInvalidProjects(t *testing.T) {
	t.Parallel()
	var all []invalidCase
	for _, group := range [][]invalidCase{resolutionCases(), nodeCases(), declarationCases(), policyCases()} {
		all = append(all, group...)
	}
	for _, tc := range all {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := fixture(t)
			if tc.edit != nil {
				tc.edit(t, m)
			}
			opts := DefaultOptions()
			if tc.opts != nil {
				tc.opts(t, &opts)
			}
			res := Compile(m, opts)
			wantDiag(t, res, tc.code, tc.file, tc.ptr)
			if res.Diagnostics.HasErrors() && (res.App != nil || res.Plugins != nil) {
				t.Error("bundles were produced despite errors")
			}
		})
	}
}
