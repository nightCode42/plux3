// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package plxerr

// registry holds every definition in ascending code order. It is read-only:
// nothing assigns to it after initialisation, and callers receive copies.
var registry = []Definition{
	// Schema and validation: structure.
	{
		UnknownProperty, "UNKNOWN_PROPERTY", SeverityError, "Unknown property",
		"The document contains a property that its schema does not define. Only properties prefixed with `x-` may be added freely; they are preserved and ignored by the compiler (SCH-004).",
		"Remove the property, correct its spelling, or prefix it with `x-` to keep it as an extension.", false,
	},
	{
		MissingProperty, "MISSING_PROPERTY", SeverityError, "Missing required property",
		"A property that the schema requires is absent.",
		"Add the property with a valid value.", false,
	},
	{
		WrongJSONType, "WRONG_JSON_TYPE", SeverityError, "Wrong JSON type",
		"A value has a different JSON type from the one the schema allows, for example a number where a string is expected.",
		"Change the value to the expected type.", false,
	},
	{
		InvalidEnumValue, "INVALID_ENUM_VALUE", SeverityError, "Value not allowed",
		"The value is not one of the values the schema allows.",
		"Use one of the allowed values listed in the message.", false,
	},
	{
		InvalidFormat, "INVALID_FORMAT", SeverityError, "Invalid format",
		"A string does not have the required format, such as a UUIDv7 identifier, a lower-kebab key, a BCP 47 locale or a semantic version.",
		"Correct the string to the documented format.", false,
	},
	{
		OutOfRange, "OUT_OF_RANGE", SeverityError, "Value out of range",
		"A number, a string length or an item count is outside the bounds the schema allows.",
		"Choose a value within the bounds given in the message.", false,
	},
	{
		InvalidStructure, "INVALID_STRUCTURE", SeverityError, "Invalid structure",
		"The value matches none of the forms the schema allows, for example a prop value that is neither a literal nor exactly one of `$expr`, `$token`, `$t` or `$asset` (SCH-011).",
		"Restructure the value into one of the allowed forms.", false,
	},
	{
		InvalidJSON, "INVALID_JSON", SeverityError, "Invalid JSON",
		"The file is not well-formed JSON: a syntax error, a duplicate key in one object, invalid UTF-8 or an unpaired surrogate escape.",
		"Fix the file at the reported position; each key may appear only once per object.", false,
	},
	{
		UnsupportedSchemaVersion, "UNSUPPORTED_SCHEMA_VERSION", SeverityError, "Unsupported schema version",
		"The document's `schemaVersion` is not a released version, or is newer than this compiler understands (SCH-000).",
		"Upgrade the Plux CLI or server, or correct `schemaVersion`.", false,
	},
	{
		MigrationFailed, "MIGRATION_FAILED", SeverityError, "Schema migration failed",
		"The document could not be migrated from its `schemaVersion` to the current version (SCH-043).",
		"Report the document to the Plux maintainers: migrations must accept every valid document of a released version.", false,
	},
	{
		DuplicateID, "DUPLICATE_ID", SeverityError, "Duplicate identifier",
		"Two entities in the project share an identifier. Identifiers are unique and immutable, and every reference uses them (SCH-002).",
		"Give the copied entity a fresh UUIDv7. Inserting a template assigns fresh identifiers automatically (SCH-031).", false,
	},
	{
		InvalidProjectLayout, "INVALID_PROJECT_LAYOUT", SeverityError, "Invalid project layout",
		"A file is not where the project layout expects a document of its kind, its name does not match its key, or a required file is missing (SCH-006).",
		"Move or rename the file as described in the document-model reference.", false,
	},
	{
		RequestTooLarge, "REQUEST_TOO_LARGE", SeverityError, "Request too large",
		"The request body, or a value inside it, is larger than the limit the installation allows. Limits bound every input so that one caller cannot exhaust the server (SEC-104).",
		"Send less in one call, or ask an administrator to raise the limit for this installation.", false,
	},
	{
		RevisionConflict, "REVISION_CONFLICT", SeverityError, "The document changed",
		"The document has been written since the revision this call is based on, so applying the change would overwrite that work (SRV-030).",
		"Read the document again, reapply the change to the current revision, and send it.", false,
	},
	{
		IdempotencyConflict, "IDEMPOTENCY_CONFLICT", SeverityError, "Idempotency key reused",
		"The idempotency key was used before with a different request. A key identifies one request, so that a retry returns the original result rather than acting twice (SRV-005).",
		"Use a new idempotency key for a different request, or repeat the original request unchanged.", false,
	},
	{
		InvalidPageToken, "INVALID_PAGE_TOKEN", SeverityError, "Invalid page token",
		"The page token is not one this server issued, or it belongs to a different filter or ordering. Tokens are integrity-protected and bound to the query they continue (SRV-004).",
		"Start the list again without a page token.", false,
	},

	// Schema and validation: references and semantics.
	{
		DuplicateKey, "DUPLICATE_KEY", SeverityError, "Duplicate key",
		"Two entities with the same parent share a key. Keys are unique within their parent (SCH-002).",
		"Rename one of them.", false,
	},
	{
		UnresolvedReference, "UNRESOLVED_REFERENCE", SeverityError, "Unresolved reference",
		"A reference names an identifier that does not exist in the project: a page, component, action graph, translation key, design token, asset, state entry, data source, collection, function or native route.",
		"Correct the identifier, or create the entity it refers to.", false,
	},
	{
		DuplicateRouteName, "DUPLICATE_ROUTE_NAME", SeverityError, "Duplicate route name",
		"Two pages or native routes use the same route name. Route names are unique across the whole app, because callers address screens by route name only (SCH-025).",
		"Rename one of the routes.", false,
	},
	{
		UnknownWidgetType, "UNKNOWN_WIDGET_TYPE", SeverityError, "Unknown widget type",
		"A node's `type` is neither a registered widget (WGT-001) nor a native slot declared in the native catalogue.",
		"Use a widget from the catalogue, or expose the host widget as a native slot.", false,
	},
	{
		UnknownProp, "UNKNOWN_PROP", SeverityError, "Unknown prop",
		"A node sets a prop that its widget's descriptor does not declare.",
		"Remove the prop or correct its name; the widget reference lists every supported prop.", false,
	},
	{
		PropTypeMismatch, "PROP_TYPE_MISMATCH", SeverityError, "Prop type mismatch",
		"A prop's literal or binding does not have the type its descriptor declares.",
		"Provide a value of the declared type, or convert the binding explicitly.", false,
	},
	{
		MissingRequiredProp, "MISSING_REQUIRED_PROP", SeverityError, "Missing required prop",
		"The widget's descriptor marks a prop as required and the node does not set it.",
		"Set the prop.", false,
	},
	{
		UnknownEvent, "UNKNOWN_EVENT", SeverityError, "Unknown event",
		"A node handles an event that its widget does not emit.",
		"Remove the handler or correct the event name.", false,
	},
	{
		InvalidChildren, "INVALID_CHILDREN", SeverityError, "Invalid children",
		"The node has `children` although its widget takes named slots or no children, or it has more or fewer children than the widget allows.",
		"Use the widget's slots, or adjust the number of children.", false,
	},
	{
		UnknownSlot, "UNKNOWN_SLOT", SeverityError, "Unknown slot",
		"A node fills a slot that its widget does not declare.",
		"Use one of the widget's declared slots.", false,
	},
	{
		MissingRequiredSlot, "MISSING_REQUIRED_SLOT", SeverityError, "Missing required slot",
		"The widget's descriptor requires a slot that the node does not fill.",
		"Fill the slot.", false,
	},
	{
		UnknownAction, "UNKNOWN_ACTION", SeverityError, "Unknown action",
		"An action-graph step uses an action that is neither built in (spec Appendix D) nor a custom action declared in the native catalogue.",
		"Use a catalogued action, or register the custom action in the host app.", false,
	},
	{
		InvalidActionGraph, "INVALID_ACTION_GRAPH", SeverityError, "Invalid action graph",
		"An action graph has a cycle, an edge to a missing step, an unreachable step or no entry step (ACT-001).",
		"Make the graph acyclic, starting at its first step, with every edge naming an existing step.", false,
	},
	{
		ComponentVersionNotFound, "COMPONENT_VERSION_NOT_FOUND", SeverityError, "Component version not found",
		"An instance references a version of a component that the project does not contain (SCH-030).",
		"Reference the component's current version.", false,
	},
	{
		UnknownType, "UNKNOWN_TYPE", SeverityError, "Unknown type",
		"A type expression names a type that is neither built in (SCH-010) nor declared in the app or plugin.",
		"Declare the named type, or correct its name.", false,
	},
	{
		InvalidTypeExpression, "INVALID_TYPE_EXPRESSION", SeverityError, "Invalid type expression",
		"A type expression is not well formed, for example `list<` without a closing bracket.",
		"Write the type as the type grammar describes, e.g. `list<string>`, `map<string,int>` or `decimal?`.", false,
	},
	{
		ValueTypeMismatch, "VALUE_TYPE_MISMATCH", SeverityError, "Value does not match its declared type",
		"A default value, mock value or parameter value does not have its declared type.",
		"Change the value, or the declared type.", false,
	},
	{
		MissingMock, "MISSING_MOCK", SeverityError, "Missing design-time mock",
		"A data source or page parameter has no design-time mock, so Studio and tests cannot render the page without a live backend (SCH-024).",
		"Add a mock value of the declared type.", false,
	},
	{
		RuntimeTooOld, "RUNTIME_TOO_OLD", SeverityError, "Newer than the minimum runtime",
		"A node uses a widget, prop, event or slot introduced after the app's `minRuntimeVersion`, and the app does not allow raising the required features (WGT-004).",
		"Raise `minRuntimeVersion`, avoid the newer member, or allow raising required features in the app document.", false,
	},
	{
		RequiredFeaturesRaised, "REQUIRED_FEATURES_RAISED", SeverityWarning, "Required features raised",
		"The release uses members newer than the app's `minRuntimeVersion`, so the compiler added required features; devices with older runtimes keep their last compatible release (WGT-004).",
		"Check how many installed devices are affected before releasing.", false,
	},
	{
		ConstraintViolation, "CONSTRAINT_VIOLATION", SeverityError, "Prop constraint violated",
		"A literal prop value violates a constraint in its descriptor, such as a minimum, a maximum or a pattern.",
		"Choose a value that meets the constraint given in the message.", false,
	},
	{
		DeprecatedMember, "DEPRECATED_MEMBER", SeverityWarning, "Deprecated widget or prop",
		"The node uses a widget, prop, event or slot that its descriptor marks as deprecated.",
		"Replace it as the deprecation note describes.", false,
	},
	{
		UnknownRoute, "UNKNOWN_ROUTE", SeverityError, "Unknown route",
		"A navigate action targets a route name that no page and no native route declares.",
		"Correct the route name, or add the page.", false,
	},
	{
		RouteParameterMissing, "ROUTE_PARAMETER_MISSING", SeverityError, "Route parameter missing at navigate",
		"A navigate action does not supply a required parameter of the target page or native route.",
		"Pass the parameter in the action's `params`.", false,
	},
	{
		RouteParameterTypeInvalid, "ROUTE_PARAMETER_TYPE_INVALID", SeverityError, "Route parameter has the wrong type",
		"A navigate action passes a parameter value whose type differs from the parameter's declared type.",
		"Pass a value of the declared type.", false,
	},
	{
		UnknownRouteParameter, "UNKNOWN_ROUTE_PARAMETER", SeverityError, "Unknown route parameter",
		"A navigate action passes a parameter that the target page or native route does not declare.",
		"Remove the parameter, or declare it on the target.", false,
	},
	{
		RedirectLoop, "REDIRECT_LOOP", SeverityError, "Redirect loop",
		"Pages redirect to each other unconditionally from `onEnter` in a cycle, so navigation would never settle.",
		"Break the cycle by guarding one of the redirects with a condition.", false,
	},

	// Schema and validation: limits and budgets.
	{
		PageNodeBudget, "PAGE_NODE_BUDGET", SeverityWarning, "Page exceeds its node budget",
		"The page has more nodes than its budget. Above the warning threshold it is reported; above the hard budget publication fails (CMP-040, spec §30.3).",
		"Split the page, move repeated content into a list with an item template, or reuse a component.", false,
	},
	{
		PageDepthBudget, "PAGE_DEPTH_BUDGET", SeverityWarning, "Page tree too deep",
		"The page's node tree is deeper than its budget (CMP-040).",
		"Flatten redundant wrappers, or extract subtrees into components.", false,
	},
	{
		PageBuildCostBudget, "PAGE_BUILD_COST_BUDGET", SeverityWarning, "Page build cost too high",
		"The page's estimated build cost, the sum of its widgets' cost hints, exceeds its budget (CMP-040).",
		"Replace eagerly built content with lazily built lists, or split the page.", false,
	},
	{
		PageImageBudget, "PAGE_IMAGE_BUDGET", SeverityWarning, "Page images too large",
		"The images bundled for the page exceed their byte budget (CMP-040).",
		"Use smaller images or load them from the network.", false,
	},
	{
		PageAnimationBudget, "PAGE_ANIMATION_BUDGET", SeverityWarning, "Too many animations",
		"The page can run more concurrent animations than its budget (CMP-040).",
		"Reduce the number of simultaneous animations.", false,
	},
	{
		LimitExceeded, "LIMIT_EXCEEDED", SeverityError, "Limit exceeded",
		"A document or release exceeds a limit from the limits registry, such as pages per plugin, string prop size or bundle size (spec §30.4).",
		"Reduce the size, or ask an administrator to raise the limit at a higher level.", false,
	},
	{
		LimitApproaching, "LIMIT_APPROACHING", SeverityWarning, "Limit nearly reached",
		"A value has reached its warning threshold, by default 80% of its limit (LIM-003).",
		"Plan to reduce the size before the limit is reached.", false,
	},

	// Schema and validation: accessibility, security and store policy.
	{
		AccessibleNameMissing, "ACCESSIBLE_NAME_MISSING", SeverityWarning, "Interactive node without accessible name",
		"An interactive node has no semantics label and no visible text from which one can be derived, so screen readers cannot announce it (WGT-013, A11Y-002).",
		"Set `semantics.label`, or give the node visible text.", false,
	},
	{
		SecretLikeValue, "SECRET_LIKE_VALUE", SeverityError, "Secret-like value detected",
		"A literal looks like an API key, token, private key or password. Bundles are readable on devices, so secrets must never be shipped in them (DAT-003).",
		"Keep the secret on the server and use it through a Plux Function or the host's auth delegate.", false,
	},
	{
		SensitiveValueExposed, "SENSITIVE_VALUE_EXPOSED", SeverityError, "Sensitive value exposed",
		"A value tagged `sensitive` flows into analytics, logs or another sink where sensitive data is forbidden (SCH-012).",
		"Remove the value from the sink, or pass a non-sensitive derivative such as a masked form.", false,
	},
	{
		InsecureURL, "INSECURE_URL", SeverityError, "Insecure URL",
		"A literal URL uses `http:`. All network access from Plux uses TLS.",
		"Use an `https:` URL.", false,
	},
	{
		ExecutableContent, "EXECUTABLE_CONTENT", SeverityError, "Executable content not allowed",
		"A document or asset carries code in a form that would run outside the PXL VM and the action interpreter (SEC-054).",
		"Express the logic with actions and PXL, or as a Plux Function.", false,
	},

	// Compiler and PXL.
	{
		PXLSyntaxError, "PXL_SYNTAX_ERROR", SeverityError, "PXL syntax error",
		"The expression does not follow the PXL grammar at the reported position.",
		"Correct the expression; the PXL reference describes the grammar.", false,
	},
	{
		PXLTypeMismatch, "PXL_TYPE_MISMATCH", SeverityError, "PXL type mismatch",
		"An operator, function or use site receives a value of a type it does not accept.",
		"Convert the value explicitly, e.g. `double(x)` or `decimal(x)`, or use a matching operator.", false,
	},
	{
		PXLUnknownIdentifier, "PXL_UNKNOWN_IDENTIFIER", SeverityError, "Unknown identifier",
		"The expression uses a root or variable that is not available at this use site (spec Appendix E.2).",
		"Use a root available here, such as `page`, `params` or `item`, or declare the state entry.", false,
	},
	{
		PXLUnknownFunction, "PXL_UNKNOWN_FUNCTION", SeverityError, "Unknown function",
		"The expression calls a function that is not in the PXL standard library.",
		"Correct the function name; the PXL reference lists every function.", false,
	},
	{
		PXLWrongArgumentCount, "PXL_WRONG_ARGUMENT_COUNT", SeverityError, "Wrong number of arguments",
		"A function or macro is called with more or fewer arguments than it takes.",
		"Pass the arguments given by the function's signature.", false,
	},
	{
		PXLNullableAccess, "PXL_NULLABLE_ACCESS", SeverityError, "Access on a nullable value",
		"The expression reads a field of, or calls a function on, a value that may be null.",
		"Use null-safe navigation (`a?.b`) or provide a default (`a ?? b`).", false,
	},
	{
		PXLDecimalDivision, "PXL_DECIMAL_DIVISION", SeverityError, "Decimal division needs a scale and rounding mode",
		"Decimal division can produce infinitely many digits, so PXL requires the scale and rounding mode to be stated (PXL-005).",
		"Write `div(a, b, scale, mode)`, e.g. `div(total, 3d, 2, \"halfEven\")`.", false,
	},
	{
		PXLCurrencyMismatch, "PXL_CURRENCY_MISMATCH", SeverityError, "Currency mismatch",
		"Money values in different currencies are added, subtracted or compared.",
		"Convert one amount into the other currency before combining them.", false,
	},
	{
		PXLInvalidLiteral, "PXL_INVALID_LITERAL", SeverityError, "Invalid literal",
		"A literal is malformed or out of range, such as an integer beyond 64 bits or an invalid string escape.",
		"Correct the literal.", false,
	},
	{
		PXLBudgetExceeded, "PXL_BUDGET_EXCEEDED", SeverityError, "Operation budget exceeded at compile time",
		"Evaluating a constant expression at compile time needed more operations than the budget allows (PXL-001).",
		"Simplify the expression.", false,
	},
	{
		PXLConstantError, "PXL_CONSTANT_ERROR", SeverityError, "Constant expression always fails",
		"The expression is constant, and evaluating it at compile time fails, for example by dividing by zero or overflowing.",
		"Correct the expression so it evaluates without error.", false,
	},
	{
		PXLUnknownField, "PXL_UNKNOWN_FIELD", SeverityError, "Unknown field",
		"The expression reads a field that the value's type does not declare.",
		"Correct the field name, or declare the field in the type.", false,
	},
	{
		PXLFeatureRequired, "PXL_FEATURE_REQUIRED", SeverityInfo, "Function needs a runtime feature",
		"The expression calls a standard-library function that runtimes evaluate from a later feature, such as locale-aware formatting. The bundle declares the feature, and runtimes without it keep their last compatible release (ADR-0009).",
		"No action is needed if the app's installed runtimes support the feature.", false,
	},
	{
		PXLUnknownEnumMember, "PXL_UNKNOWN_ENUM_MEMBER", SeverityError, "Unknown enum member",
		"A string compared with an enum value is not one of the enum's members.",
		"Use one of the members listed in the message.", false,
	},
	{
		PXLInvalidMacro, "PXL_INVALID_MACRO", SeverityError, "Invalid macro call",
		"A macro such as `map` or `filter` needs a variable name and an expression over it, called on a list, e.g. `items.map(x, x.price)`.",
		"Call the macro on a list with a variable name and an expression.", false,
	},
	{
		PXLExpressionTooComplex, "PXL_EXPRESSION_TOO_COMPLEX", SeverityError, "Expression too complex",
		"The expression exceeds the length or nesting depth the compiler accepts.",
		"Split the logic into computed state entries or a Plux Function.", false,
	},
	{
		InternalCompilerError, "INTERNAL_COMPILER_ERROR", SeverityError, "Internal compiler error",
		"The compiler met an unexpected condition. This is a bug: the compiler must report problems as diagnostics and never crash (CMP-052).",
		"Report the input to the Plux maintainers.", false,
	},
	{
		ContentIDCollision, "CONTENT_ID_COLLISION", SeverityError, "Content identifier collision",
		"Two different PXL programs or style objects received the same 64-bit content identifier (ADR-0002). This is extremely unlikely.",
		"Report the input to the Plux maintainers; changing either value avoids the collision.", false,
	},

	// Release and sync.
	{
		ManifestSignatureInvalid, "MANIFEST_SIGNATURE_INVALID", SeverityError, "Manifest signature invalid",
		"The manifest is not signed by a key the app trusts, is not in canonical form, or names another app, environment or channel (SEC-052, ADR-0029).",
		"The device keeps its current release. Check that the app embeds the environment's keys from `plux pull`, and that the server signs with that environment's key.", false,
	},
	{
		ManifestExpired, "MANIFEST_EXPIRED", SeverityError, "Manifest expired",
		"The manifest's expiry has passed by the device's clock: the server has stopped re-signing it, a network path is replaying an old one, or the device clock is ahead (SEC-052).",
		"The device keeps its current release. Check that the worker role is running and re-signing manifests; check the device clock.", false,
	},
	{
		RollbackRejected, "ROLLBACK_REJECTED", SeverityError, "Rollback attempt rejected",
		"The manifest's release sequence is lower than one this device has already accepted for the channel (SEC-055). Rollbacks are published as new, higher sequences (REL-006).",
		"The device keeps its current release. Roll back by promoting the earlier content as a new release.", false,
	},
	{
		UnsupportedRequiredFeature, "UNSUPPORTED_REQUIRED_FEATURE", SeverityError, "Unsupported required feature",
		"The bundle requires a feature that this runtime does not support (BND-008).",
		"Devices keep their last compatible release. Update the app to a runtime with the feature, or avoid the feature in the release.", false,
	},
	{
		PatchHashMismatch, "PATCH_HASH_MISMATCH", SeverityError, "Hash mismatch after patch",
		"A section or bundle rebuilt from a delta does not have the hash the delta and the manifest name: the delta was made against another base, or it is corrupt (ADR-0003, SYN-011).",
		"Download the full bundle instead of the delta.", false,
	},
	{
		DeltaMalformed, "DELTA_MALFORMED", SeverityError, "Malformed delta",
		"The delta's header or instructions are invalid: wrong magic or version, a truncated instruction, an unknown operation or a size beyond the limits (ADR-0003).",
		"Download the full bundle instead of the delta.", false,
	},
	{
		RevertedToLastKnownGood, "REVERTED_TO_LAST_KNOWN_GOOD", SeverityError, "Reverted to last known good release",
		"A newly activated release caused three or more fatal errors or crashes within its first two launches, so the device went back to the previous release and pinned it until a newer sequence arrives (SYN-006).",
		"Inspect the release's error reports, fix the cause and publish a new release; devices move on automatically.", false,
	},
	{
		DiskQuotaExceeded, "DISK_QUOTA_EXCEEDED", SeverityError, "Disk quota exceeded",
		"Staging the release would exceed the device's disk quota for Plux (`device.diskQuota`), or the device ran out of storage while writing it. The active release is untouched (SYN-012).",
		"Reduce the release's size, raise the quota in the app's limits, or free storage on the device.", false,
	},
	{
		BundleMalformed, "BUNDLE_MALFORMED", SeverityError, "Malformed bundle",
		"The container header or section directory is invalid: wrong magic or version, unknown flags, overlapping or misaligned sections, a size mismatch or a header hash mismatch (BND-003).",
		"Rebuild the bundle. A malformed bundle is never loaded.", false,
	},
	{
		SectionHashMismatch, "SECTION_HASH_MISMATCH", SeverityError, "Section hash mismatch",
		"A section's SHA-256 differs from the hash in the directory: the bundle is corrupt or has been tampered with (BND-005).",
		"Download or rebuild the bundle.", false,
	},
	{
		SectionVerificationFailed, "SECTION_VERIFICATION_FAILED", SeverityError, "Section failed verification",
		"A section is not a well-formed FlatBuffers buffer of its kind, or exceeds the verifier's limits (BND-006).",
		"Rebuild the bundle; a section that fails verification is never read.", false,
	},
	{
		BundleEncryptedUnsupported, "BUNDLE_ENCRYPTED_UNSUPPORTED", SeverityError, "Encrypted bundle not supported",
		"The bundle is marked as encrypted, which this reader does not support (SEC-053).",
		"Use a reader that supports confidential bundles.", false,
	},
	{
		TransportDecodingFailed, "TRANSPORT_DECODING_FAILED", SeverityError, "Transport decoding failed",
		"The zstd transport encoding is corrupt, or it decompresses to more than the declared size or the configured limit (BND-007).",
		"Download the bundle again.", false,
	},
	{
		SyncFailed, "SYNC_FAILED", SeverityError, "Sync failed",
		"The runtime could not complete a sync: the server was unreachable or answered with an error, a download failed after its retries, or a bundle failed verification after the full-bundle retry (SYN-010, SYN-011). The cause is in the error's details.",
		"The device keeps its current release and retries at the next start or manual sync. Check the server's health and the device's connectivity.", false,
	},

	// Runtime rendering and navigation.
	{
		NodeBuildFailed, "NODE_BUILD_FAILED", SeverityError, "Build error in node",
		"Building, laying out or painting a node failed, or a value it needs could not be decoded or evaluated. The page's error boundary rendered its fallback (RT-020).",
		"Look up the node path in the report and fix the page; the source map of a development bundle names the document location.", false,
	},
	{
		PropValueInvalid, "PROP_VALUE_INVALID", SeverityWarning, "Prop value not usable",
		"A prop's value could not be decoded as its type, or its binding failed to evaluate. The prop took its declared default and the node rendered (ADR-0031).",
		"Look up the node path in the report and fix the value or the expression; the source map of a development bundle names the document location.", false,
	},
	{
		UnknownWidget, "UNKNOWN_WIDGET", SeverityError, "Unknown widget type",
		"A node names a widget type this runtime does not know. A neutral placeholder is shown instead (WGT-014).",
		"Raise the app's minimum runtime version for the widget, or update the host app to a newer runtime.", false,
	},
	{
		ActionsNotAvailable, "ACTIONS_NOT_AVAILABLE", SeverityWarning, "Actions not available in this runtime",
		"An event handler fired, but this runtime renders pages without running actions; actions arrive with the action executor in phase 5 (ADR-0031).",
		"Nothing to fix in the page; the handler runs once the runtime supports actions.", false,
	},
	{
		PluginDisabled, "PLUGIN_DISABLED", SeverityWarning, "Plugin switched off",
		"The release's control switches turn the plugin off (kill switch), so every route into it shows its fallback page (RT-022).",
		"Turn the switch off in the release's channel controls once the problem is fixed.", false,
	},

	// Security.
	{
		OutboundRequestBlocked, "OUTBOUND_REQUEST_BLOCKED", SeverityError, "Outbound request blocked",
		"A request to a user-supplied URL would have reached a private, loopback, link-local or metadata address. Those are refused unless the installation allows them explicitly (SEC-105).",
		"Use a publicly reachable address, or ask an administrator to allow the range this installation should reach.", false,
	},
	{
		AssetRejected, "ASSET_REJECTED", SeverityError, "Asset rejected by the malware scanner",
		"The installation's malware scanner found something in the uploaded file (SRV-060).",
		"Check the file on a trusted machine and upload a clean copy.", false,
	},

	// Governance.
	{
		MultiFactorRequired, "MULTI_FACTOR_REQUIRED", SeverityError, "Second factor required",
		"The capability this call needs — publishing, approving, managing keys or managing members — requires a second factor, and the session has not completed one (SEC-100).",
		"Complete the second factor and repeat the call.", false,
	},
	{
		AuthenticationRequired, "AUTHENTICATION_REQUIRED", SeverityError, "Authentication required",
		"The call carried no credential, or one that has expired or been revoked.",
		"Sign in again, or use a valid access token.", false,
	},
	{
		EditingLockHeld, "EDITING_LOCK_HELD", SeverityError, "Editing lock held by another user",
		"Editing a plugin requires holding its lock, and someone else holds it. The lock expires two minutes after the holder's last heartbeat (SRV-040).",
		"Ask the holder for the lock, wait for it to expire, or take it over if you may (SRV-041).", false,
	},
	{
		PermissionDenied, "PERMISSION_DENIED", SeverityError, "Permission denied",
		"The caller does not hold the permission this call needs on this resource. Authorisation is deny-by-default (SEC-102).",
		"Ask an administrator of the organisation for the permission the message names.", false,
	},
	{
		ResourceNotFound, "RESOURCE_NOT_FOUND", SeverityError, "Not found",
		"The resource does not exist, or the caller may not see it. The two are reported the same way, so that the API does not disclose what exists in another organisation.",
		"Check the identifier, and that you have access to the organisation that owns it.", false,
	},
	{
		ResourceExists, "RESOURCE_EXISTS", SeverityError, "Already exists",
		"Something with this key already exists where keys must be unique, such as an organisation, team, app, environment or channel key.",
		"Choose another key, or use the existing resource.", false,
	},
	{
		PreconditionFailed, "PRECONDITION_FAILED", SeverityError, "Precondition failed",
		"The call is valid but the resource is not in a state that allows it, such as removing an organisation's last owner or accepting an invitation that has expired.",
		"Read the message for the state that blocks the call, change it, and repeat the call.", false,
	},
	{
		RateLimited, "RATE_LIMITED", SeverityError, "Rate limit exceeded",
		"The caller has made more requests than the limit for this principal, device or address allows (SRV-065).",
		"Retry after the interval the response reports.", false,
	},
	{
		ReleaseInconsistent, "RELEASE_INCONSISTENT", SeverityError, "Versions compiled against different sources",
		"A plugin version in the release was compiled against app-level documents or assets other than the release's, so its bundle would differ if it were compiled with them (REL-003).",
		"Publish the plugin again against the current app version, then create the release.", false,
	},
	{
		WarningsNotAcknowledged, "WARNINGS_NOT_ACKNOWLEDGED", SeverityError, "Warnings not acknowledged",
		"The publish found warnings, and the publisher did not acknowledge them (SRV-051).",
		"Fix the warnings, or publish again acknowledging them.", false,
	},
	{
		PluginNotPublished, "PLUGIN_NOT_PUBLISHED", SeverityError, "Plugin has no published version",
		"An app release holds exactly one version of every active plugin and one app bundle, and this one has none (REL-002).",
		"Publish the plugin, or delete it, before creating the release.", false,
	},
	{
		InternalServerError, "INTERNAL_SERVER_ERROR", SeverityError, "Internal error",
		"The server failed in a way it does not recognise. The incident identifier in the message appears in the server's logs; nothing else about the failure is returned.",
		"Retry the call. If it keeps failing, give the incident identifier to the operator of the installation.", false,
	},
	{
		UpstreamUnavailable, "UPSTREAM_UNAVAILABLE", SeverityError, "A service the server depends on is unavailable",
		"The server could not reach a service outside it that the call needs, such as the single sign-on provider.",
		"Retry later. If it persists, the operator checks the service and the server's outbound network.", false,
	},

	// Studio, CLI and AI.
	{
		CLIConfigurationInvalid, "CLI_CONFIGURATION_INVALID", SeverityError, "Invalid CLI configuration",
		"A command-line argument, flag or configuration value is invalid.",
		"Run the command with `--help` to see the accepted arguments.", false,
	},
	{
		ProjectNotFound, "PROJECT_NOT_FOUND", SeverityError, "Project not found",
		"The directory does not contain a Plux project: `app.json` is missing (SCH-006).",
		"Run the command in a project directory, or pass the project's path.", false,
	},
	{
		FileSystemError, "FILE_SYSTEM_ERROR", SeverityError, "File system error",
		"A file or directory could not be read or written.",
		"Check that the path exists and is accessible.", false,
	},
}
