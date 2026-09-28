// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package plxerr

import "sort"

// Registered codes. A code and its reason are permanent (ADR-0018): a retired
// code keeps its definition, marked deprecated, and is never reassigned.
const (
	// Schema and validation: structure (PLX-1000–1099).

	UnknownProperty          Code = 1001
	MissingProperty          Code = 1002
	WrongJSONType            Code = 1003
	InvalidEnumValue         Code = 1004
	InvalidFormat            Code = 1005
	OutOfRange               Code = 1006
	InvalidStructure         Code = 1007
	InvalidJSON              Code = 1008
	UnsupportedSchemaVersion Code = 1009
	MigrationFailed          Code = 1010
	DuplicateID              Code = 1011
	InvalidProjectLayout     Code = 1020
	RequestTooLarge          Code = 1021
	RevisionConflict         Code = 1030
	IdempotencyConflict      Code = 1031
	InvalidPageToken         Code = 1032

	// Schema and validation: references and semantics (PLX-1100–1199).

	DuplicateKey              Code = 1101
	UnresolvedReference       Code = 1102
	DuplicateRouteName        Code = 1103
	UnknownWidgetType         Code = 1104
	UnknownProp               Code = 1105
	PropTypeMismatch          Code = 1106
	MissingRequiredProp       Code = 1107
	UnknownEvent              Code = 1108
	InvalidChildren           Code = 1109
	UnknownSlot               Code = 1110
	MissingRequiredSlot       Code = 1111
	UnknownAction             Code = 1112
	InvalidActionGraph        Code = 1113
	ComponentVersionNotFound  Code = 1114
	UnknownType               Code = 1115
	InvalidTypeExpression     Code = 1116
	ValueTypeMismatch         Code = 1117
	MissingMock               Code = 1118
	RuntimeTooOld             Code = 1119
	RequiredFeaturesRaised    Code = 1120
	ConstraintViolation       Code = 1121
	DeprecatedMember          Code = 1122
	UnknownRoute              Code = 1201
	RouteParameterMissing     Code = 1203
	RouteParameterTypeInvalid Code = 1204
	UnknownRouteParameter     Code = 1205
	RedirectLoop              Code = 1206

	// Schema and validation: limits and budgets (PLX-1300–1399).

	PageNodeBudget      Code = 1310
	PageDepthBudget     Code = 1311
	PageBuildCostBudget Code = 1312
	PageImageBudget     Code = 1313
	PageAnimationBudget Code = 1314
	LimitExceeded       Code = 1320
	LimitApproaching    Code = 1321

	// Schema and validation: accessibility, security and store policy
	// (PLX-1400–1599).

	AccessibleNameMissing Code = 1401
	SecretLikeValue       Code = 1500
	SensitiveValueExposed Code = 1501
	InsecureURL           Code = 1502
	ExecutableContent     Code = 1503

	// Compiler and PXL (PLX-2000–2999).

	PXLSyntaxError          Code = 2001
	PXLTypeMismatch         Code = 2002
	PXLUnknownIdentifier    Code = 2003
	PXLUnknownFunction      Code = 2004
	PXLWrongArgumentCount   Code = 2005
	PXLNullableAccess       Code = 2006
	PXLDecimalDivision      Code = 2007
	PXLCurrencyMismatch     Code = 2008
	PXLInvalidLiteral       Code = 2009
	PXLBudgetExceeded       Code = 2010
	PXLConstantError        Code = 2011
	PXLUnknownField         Code = 2012
	PXLFeatureRequired      Code = 2013
	PXLUnknownEnumMember    Code = 2014
	PXLInvalidMacro         Code = 2015
	PXLExpressionTooComplex Code = 2016
	InternalCompilerError   Code = 2201
	ContentIDCollision      Code = 2202

	// Release and sync (PLX-3000–3999).

	UnsupportedRequiredFeature Code = 3010
	BundleMalformed            Code = 3040
	SectionHashMismatch        Code = 3041
	SectionVerificationFailed  Code = 3042
	BundleEncryptedUnsupported Code = 3043
	TransportDecodingFailed    Code = 3044

	// Security (PLX-6000–6999).

	OutboundRequestBlocked Code = 6030
	AssetRejected          Code = 6031

	// Governance (PLX-8000–8999).

	MultiFactorRequired     Code = 8011
	AuthenticationRequired  Code = 8012
	EditingLockHeld         Code = 8020
	PermissionDenied        Code = 8030
	ResourceNotFound        Code = 8031
	ResourceExists          Code = 8032
	PreconditionFailed      Code = 8033
	RateLimited             Code = 8040
	ReleaseInconsistent     Code = 8050
	WarningsNotAcknowledged Code = 8051
	PluginNotPublished      Code = 8052
	InternalServerError     Code = 8090
	UpstreamUnavailable     Code = 8091

	// Studio, CLI and AI (PLX-9000–9999).

	CLIConfigurationInvalid Code = 9100
	ProjectNotFound         Code = 9101
	FileSystemError         Code = 9102
)

// Definition is the registered description of a code (DX-003).
type Definition struct {
	// Code is the stable code.
	Code Code `json:"code"`
	// Reason is the stable UPPER_SNAKE_CASE identifier.
	Reason Reason `json:"reason"`
	// Severity is the default severity of diagnostics with this code; policy
	// may raise it, never lower it.
	Severity Severity `json:"severity"`
	// Title names the problem in a few words.
	Title string `json:"title"`
	// Cause explains the likely cause in complete sentences.
	Cause string `json:"cause"`
	// Fix suggests how to resolve the problem.
	Fix string `json:"fix"`
	// Deprecated marks a retired code that is kept so it is never reused.
	Deprecated bool `json:"deprecated,omitempty"`
}

// Lookup returns the definition of a registered code.
func Lookup(c Code) (Definition, bool) {
	i := sort.Search(len(registry), func(i int) bool { return registry[i].Code >= c })
	if i < len(registry) && registry[i].Code == c {
		return registry[i], true
	}
	return Definition{}, false
}

// Definitions returns a copy of every registered definition in ascending
// code order.
func Definitions() []Definition { return append([]Definition(nil), registry...) }
