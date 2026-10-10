// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
)

// deviceFeature is required by a bundle that runs a feedback or device
// action: a runtime that does not run them refuses the bundle instead of
// failing the steps with PLX-4010 (BND-008, SEC-080). The actions first
// run in runtime 0.3.0.
const deviceFeature = "device"

// deviceActions is every feedback and device action P5 R8 delivers, with
// the device API it needs (empty for none) and the optional package that
// provides it (empty for the core runtime).
var deviceActions = map[string]struct{ api, pkg string }{
	"showSnackbar":      {},
	"showToast":         {},
	"openUrl":           {},
	"haptic":            {api: "haptics"},
	"copyToClipboard":   {api: "clipboard"},
	"share":             {api: "share"},
	"requestPermission": {},
	"pickImage":         {api: "photos", pkg: "plux_media"},
	"capturePhoto":      {api: "camera", pkg: "plux_media"},
	"pickFile":          {api: "files", pkg: "plux_media"},
	"scanCode":          {api: "camera", pkg: "plux_scanner"},
	"getLocation":       {api: "location", pkg: "plux_location"},
}

// permissionAPIs are the device APIs a platform asks the user to permit:
// the values requestPermission accepts.
var permissionAPIs = []string{"camera", "photos", "location", "contacts", "biometrics", "notifications"}

// DeviceUse is a step that runs an action of an optional package, at the
// JSON path of its action: what a host build must have installed (RT-060).
type DeviceUse struct {
	Package string
	Action  string
	File    string
	Path    string
}

// checkApproved checks that everything a plugin requests in its
// capabilities is approved by the app document (SEC-080). An app approves
// no device API it does not list; its networkDomains and functions narrow
// what plugins declare when listed.
func (u *unit) checkApproved(pl *plugin) {
	caps := pl.doc.Capabilities
	if caps == nil {
		return
	}
	approved := u.project.App.Doc.Capabilities
	if approved == nil {
		approved = &schema.ApprovedCapabilities{}
	}
	for i, api := range caps.DeviceApis {
		if !slices.Contains(approved.DeviceApis, api) {
			u.report(plxerr.CapabilityNotApproved, pl.file, plxerr.Pointer("capabilities", "deviceApis", strconv.Itoa(i)),
				"the app does not approve the device API %q", api)
		}
	}
	if approved.NetworkDomains != nil {
		for i, d := range caps.NetworkDomains {
			if !domainApproved(d, approved.NetworkDomains) {
				u.report(plxerr.CapabilityNotApproved, pl.file, plxerr.Pointer("capabilities", "networkDomains", strconv.Itoa(i)),
					"the app does not approve the network domain %q", d)
			}
		}
	}
	if approved.Functions != nil {
		for i, f := range caps.Functions {
			if !slices.Contains(approved.Functions, f.Function) {
				u.report(plxerr.CapabilityNotApproved, pl.file, plxerr.Pointer("capabilities", "functions", strconv.Itoa(i), "function"),
					"the app does not approve the function %q", f.Function)
			}
		}
	}
}

// checkNetworkPins checks the pins the app sets for customer API domains
// (SEC-042): each domain is one the app declares in networkDomains, and
// each pin is the canonical base64 of a SHA-256 hash. That a domain has at
// least two distinct pins is the document schema's minItems and
// uniqueItems.
func (u *unit) checkNetworkPins() {
	c := u.project.App.Doc.Capabilities
	if c == nil {
		return
	}
	hosts := make([]string, 0, len(c.NetworkPins))
	for h := range c.NetworkPins {
		hosts = append(hosts, h)
	}
	slices.Sort(hosts)
	for _, h := range hosts {
		ptr := plxerr.Pointer("capabilities", "networkPins", h)
		if !domainAllowed(h, c.NetworkDomains) {
			u.report(plxerr.CapabilityNotApproved, "app.json", ptr,
				"%q is pinned but not declared in capabilities.networkDomains", h)
		}
		for i, p := range c.NetworkPins[h] {
			if raw, err := base64.StdEncoding.DecodeString(p); err != nil || len(raw) != 32 || base64.StdEncoding.EncodeToString(raw) != p {
				u.report(plxerr.InvalidFormat, "app.json", plxerr.Pointer("capabilities", "networkPins", h, strconv.Itoa(i)),
					"a pin is the canonical base64 of a SHA-256 hash (44 characters)")
			}
		}
	}
}

// domainApproved reports whether a domain a plugin declares is covered by
// the approved list: the same entry, or for a plain name a wildcard
// entry above it. A wildcard is covered only by the same wildcard.
func domainApproved(domain string, approved []string) bool {
	if slices.Contains(approved, domain) {
		return true
	}
	if strings.HasPrefix(domain, "*") {
		return false
	}
	return domainAllowed(domain, approved)
}

// checkDeviceStep checks a feedback or device action step against the
// capabilities of its plugin (SEC-080): the device API it needs, the
// permission requestPermission asks for and the URL openUrl opens must be
// declared, or the runtime would block the step. An app-level graph has no
// plugin and runs under the app's approved set. A step of an optional
// package is recorded for the host build check (RT-060).
func (u *unit) checkDeviceStep(g *graph, action string, input map[string]json.RawMessage, ptr string) {
	use, ok := deviceActions[action]
	if !ok {
		return
	}
	declared := u.declaredDeviceAPIs(g.plugin)
	api := use.api
	if action == "requestPermission" {
		api = literalString(input["permission"])
	}
	if api != "" && !slices.Contains(declared, schema.DeviceAPI(api)) {
		u.report(plxerr.DeviceCapabilityUndeclared, g.file, ptr+"/action",
			"%s needs the device API %q, which the plugin does not declare in capabilities.deviceApis", action, api)
	}
	if action == "openUrl" {
		u.checkOpenURL(g, literalString(input["url"]), ptr+"/input/url")
	}
	if use.pkg != "" {
		u.deviceUses = append(u.deviceUses, DeviceUse{Package: use.pkg, Action: action, File: g.file, Path: ptr + "/action"})
	}
}

// declaredDeviceAPIs are the device APIs a plugin declares, or for the
// app's own graphs the ones the app approves.
func (u *unit) declaredDeviceAPIs(pl *plugin) []schema.DeviceAPI {
	if pl != nil {
		if c := pl.doc.Capabilities; c != nil {
			return c.DeviceApis
		}
		return nil
	}
	if c := u.project.App.Doc.Capabilities; c != nil {
		return c.DeviceApis
	}
	return nil
}

// networkDomains are the domains a plugin declares, or for the app's own
// graphs the ones the app approves.
func (u *unit) networkDomains(pl *plugin) []string {
	if pl != nil {
		if c := pl.doc.Capabilities; c != nil {
			return c.NetworkDomains
		}
		return nil
	}
	if c := u.project.App.Doc.Capabilities; c != nil {
		return c.NetworkDomains
	}
	return nil
}

// checkOpenURL checks a literal openUrl address: an HTTPS URL on a domain
// the plugin declares, or a link the app answers (NAV-008). An address
// computed at run time is checked by the runtime.
func (u *unit) checkOpenURL(g *graph, raw, ptr string) {
	if raw == "" {
		return
	}
	link, err := url.Parse(raw)
	if err != nil || link.Scheme == "" {
		u.report(plxerr.OpenURLDomainUndeclared, g.file, ptr, "%q is not an absolute URL", raw)
		return
	}
	var links *schema.DeepLinkPolicy
	if n := u.project.App.Doc.Navigation; n != nil {
		links = n.DeepLinks
	}
	scheme, host := strings.ToLower(link.Scheme), strings.ToLower(link.Hostname())
	switch {
	case links != nil && scheme == "https" && slices.Contains(links.Hosts, host):
	case links != nil && scheme != "https" && slices.Contains(links.Schemes, scheme):
	case scheme != "https":
		u.report(plxerr.OpenURLDomainUndeclared, g.file, ptr, "openUrl opens HTTPS URLs and the app's deep links, not %s: URLs", scheme)
	case !domainAllowed(host, u.networkDomains(g.plugin)):
		u.report(plxerr.OpenURLDomainUndeclared, g.file, ptr, "openUrl opens %s, which the plugin does not declare in capabilities.networkDomains", host)
	}
}

// DeviceUses lists the steps of the compiled project, in the files keep
// accepts (all when keep is nil), that run an action of an optional
// package, sorted by file and path.
func DeviceUses(res *Result, keep func(file string) bool) []DeviceUse {
	if res == nil {
		return nil
	}
	var out []DeviceUse
	for _, d := range res.DeviceUses {
		if keep == nil || keep(d.File) {
			out = append(out, d)
		}
	}
	return out
}

// HostBuildLacksPackages checks the optional packages the compiled
// project's device actions need against the packages one host build
// records in its native catalogue (RT-060, REL-080): each use of an
// action whose package the build lacks is a warning naming the package
// and the build, like HostBuildIncompatibilities for native entries. A
// build whose catalogue is unknown is not judged.
func HostBuildLacksPackages(res *Result, keep func(file string) bool, build string, catalogue *schema.NativeCatalogueDocument) plxerr.Diagnostics {
	var out plxerr.Diagnostics
	if catalogue == nil {
		return out
	}
	for _, d := range DeviceUses(res, keep) {
		if !slices.Contains(catalogue.Packages, d.Package) {
			out = append(out, plxerr.NewDiagnostic(plxerr.HostBuildLacksPackage, plxerr.Location{File: d.File, Path: d.Path},
				"host build %s does not install %s, which %s needs", build, d.Package, d.Action))
		}
	}
	out.Sort()
	return out
}

// sortedDeviceUses is the unit's device uses sorted by file and path.
func (u *unit) sortedDeviceUses() []DeviceUse {
	out := slices.Clone(u.deviceUses)
	slices.SortFunc(out, func(a, b DeviceUse) int {
		return cmp.Or(strings.Compare(a.File, b.File), strings.Compare(a.Path, b.Path))
	})
	return out
}
