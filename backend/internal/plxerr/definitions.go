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
		UnknownIcon, "UNKNOWN_ICON", SeverityError, "Unknown icon",
		"An icon names a glyph its set does not have, or computes its name, so the server cannot deliver its glyph in the release's icon font (THM-005).",
		"Use a name from the set's catalogue (docs/reference/icons.md), written literally.", false,
	},
	{
		CustomActionNamedLikeBuiltIn, "CUSTOM_ACTION_NAMED_LIKE_BUILT_IN", SeverityError, "Custom action named like a built-in action",
		"The native catalogue declares a custom action with the name of a built-in action (spec Appendix D). A step of that name always runs the built-in action, so the host's action would be reachable only through callNative (ACT-060).",
		"Rename the custom action in the host app, for example with a prefix of its own, and run plux native scan again.", false,
	},
	{
		FlowCallCycle, "FLOW_CALL_CYCLE", SeverityError, "Flows call each other in a cycle",
		"A callFlow step calls a flow that, through its own callFlow steps, calls the graph back. Action graphs are acyclic across flows too, so a run cannot recurse (ACT-001, ACT-061).",
		"Break the cycle: move the shared steps into a flow that calls neither graph.", false,
	},
	{
		FlowNotExported, "FLOW_NOT_EXPORTED", SeverityError, "Flow private to its plugin",
		"A callFlow step names a flow of another plugin as <plugin>/<flow>, and that flow is not exported. Only exported flows are callable across plugins (ACT-061).",
		"Set exported on the flow in its plugin, or call a flow of the step's own plugin by its key.", false,
	},
	{
		InvalidTrigger, "INVALID_TRIGGER", SeverityError, "Invalid trigger",
		"A trigger names something its owner cannot see — a state entry, a host event or a data source — or two timers share a name (ACT-002).",
		"Name a state entry of the page, the plugin or the app, a host event the app document declares, or a data source in scope; give each timer its own name.", false,
	},
	{
		UndeclaredComponentEvent, "UNDECLARED_COMPONENT_EVENT", SeverityError, "Component event not declared",
		"An emitEvent step names an event its component does not declare, or sends a payload to an event that declares none. A component's events are its contract with its users (SCH-030).",
		"Declare the event in the component's events, with the payload type the step sends, or correct its name.", false,
	},
	{
		EmitEventOutsideComponent, "EMIT_EVENT_OUTSIDE_COMPONENT", SeverityError, "emitEvent outside a component",
		"An emitEvent step is in a graph that does not belong to a component: a page's graph or a flow has no component events to emit.",
		"Emit host events with emitHostEvent, or move the step into a handler of the component.", false,
	},
	{
		InvalidRetryPolicy, "INVALID_RETRY_POLICY", SeverityError, "Invalid retry policy",
		"A step's retry policy cannot work as written: its maxBackoffMs is below its backoffMs, or it retries cancelled errors, which end the run whatever the step declares (ACT-006).",
		"Set maxBackoffMs to at least backoffMs, and remove cancelled from the error kinds it retries.", false,
	},
	{
		StateEntryReadOnly, "STATE_ENTRY_READ_ONLY", SeverityError, "State entry written that cannot be",
		"A setState, patchState or resetState step names a computed state entry, whose value is derived from other state and recomputed when it changes (STA-004).",
		"Write the entries the computed entry reads, or declare the entry with a default instead of a computed expression.", false,
	},
	{
		StatePatchNotObject, "STATE_PATCH_NOT_OBJECT", SeverityError, "State patch of a value that is not an object",
		"A patchState step names a state entry whose type is not a declared object type, so it has no fields to merge (STA-002).",
		"Use setState to replace the value, or declare the entry with an object type.", false,
	},
	{
		StatePersistenceNotAllowed, "STATE_PERSISTENCE_NOT_ALLOWED", SeverityError, "Persistence that the entry cannot have",
		"A computed state entry, or a run variable (an action graph's state), declares a persistence other than memory: a computed value is derived again from the state it reads, and a run variable ends with its run (STA-001, STA-003).",
		"Remove the persistence, or persist the entries the computed value reads.", false,
	},
	{
		StateMigrationRequired, "STATE_MIGRATION_REQUIRED", SeverityError, "Stored state changes type without a migration",
		"A session, persisted or secure state entry has another type than in the previous release and declares no migration, so devices could not read the value they stored (STA-040).",
		"Declare a migration on the entry: `from` the previous type with an expression `value` over `previous`, or `reset: true` to start from the default.", false,
	},
	{
		StateMigrationMismatch, "STATE_MIGRATION_MISMATCH", SeverityError, "State migration from a type the previous release did not have",
		"A state entry's migration declares `from` a type other than the entry's type in the previous release, so it would never run on the values devices stored (STA-040).",
		"Set `from` to the entry's type in the previous release, or use `reset: true`.", false,
	},
	{
		StateMigrationInvalid, "STATE_MIGRATION_INVALID", SeverityError, "State migration that can never run",
		"A state entry declares a migration although it is computed or kept only in memory, or its migration says `reset: false`, or migrates `from` the type the entry already has (STA-040).",
		"Remove the migration, or give the entry a session, persisted or secure persistence and its previous type in `from`.", false,
	},
	{
		FormValidatorNotApplicable, "FORM_VALIDATOR_NOT_APPLICABLE", SeverityError, "Validator that does not apply to its field",
		"A form field declares a validator its type cannot have: length on a value that is neither text nor a list, range or decimal precision on a value that is not a number, date range on a value that is not a date or a date-time, or regex, email, phone or IBAN on a value that is not text (STA-020, ADR-0047).",
		"Change the field's type or remove the validator; the forms reference lists the types each validator accepts.", false,
	},
	{
		FormValidatorOptions, "FORM_VALIDATOR_OPTIONS", SeverityError, "Invalid validator options",
		"A validator lacks an option its kind needs, carries one its kind does not take, or has bounds that cannot hold: a regex without a pattern, a length, range or date range without min or max, a min above its max, a decimal precision without maxScale or maxIntegerDigits, a custom validator without a rule, or an asynchronous one without a graph (STA-020).",
		"Give the validator exactly the options of its kind, as the forms reference lists them.", false,
	},
	{
		FormPatternInvalid, "FORM_PATTERN_INVALID", SeverityError, "Invalid validator pattern",
		"A regex validator's pattern is not in the pxl.regex.v1 subset of RE2 or exceeds pxl.regexPatternLength, pxl.regexProgramSize or pxl.regexRepeat; patterns are checked at publish, so the runtime never meets an invalid one (PXL-003, ADR-0047).",
		"Correct the pattern at the reported position; the PXL reference lists the supported syntax.", false,
	},
	{
		FormPhoneRegionUnknown, "FORM_PHONE_REGION_UNKNOWN", SeverityError, "Unknown phone region",
		"A phone validator names a region the phone table of pxl.phone.v1 does not know (STA-020, ADR-0047).",
		"Use an ISO 3166-1 alpha-2 region with a calling code, or omit the region to use the device locale's.", false,
	},
	{
		FormAsyncValidatorInvalid, "FORM_ASYNC_VALIDATOR_INVALID", SeverityError, "Invalid asynchronous validator",
		"An asynchronous validator's graph declares inputs, or an output other than `bool` or `string?`, or the validator belongs to a component shared across plugins, which has no graphs to run (STA-020, ADR-0047).",
		"Use a graph of the page or plugin that takes the field's value as its event and stops with true or null when the value is valid, false or a message otherwise.", false,
	},
	{
		FormFieldInitialMissing, "FORM_FIELD_INITIAL_MISSING", SeverityError, "Form field without an initial value",
		"A form field has no initial value and its type is not nullable, so the form could not start or be reset (STA-020).",
		"Give the field an initial value, such as \"\" for text, or make its type nullable.", false,
	},
	{
		FormNameConflict, "FORM_NAME_CONFLICT", SeverityError, "Form or field named twice",
		"A form has the name of another form or of a state entry of the same page or component, whose state it would replace, or two fields of a form share a name (STA-020).",
		"Rename the form or the field.", false,
	},
	{
		FormWriteInvalid, "FORM_WRITE_INVALID", SeverityError, "Form state written that cannot be",
		"A state action writes form state other than a field's value (`<form>.values.<field>`, with setState) or touched flag (`<form>.touched.<field>`, with setState): errors, dirty flags and the status follow from the validators, and the whole form changes only through resetForm (STA-020).",
		"Write the field's value or touched flag with setState, or use validateForm, submitForm or resetForm.", false,
	},
	{
		DataSourceConfigInvalid, "DATA_SOURCE_CONFIG_INVALID", SeverityError, "Invalid data source configuration",
		"The configuration of a REST or GraphQL data source, or of one of its operations, is not what the data layer runs: a property is unknown or missing, the method, path or GraphQL document is malformed, the base URL names no string variable, a cache policy or TTL is invalid, or an operation's input or output type is unknown (DAT-001, DAT-003, DAT-010, ADR-0048).",
		"Correct the configuration as the data sources reference describes; the message names the property.", false,
	},
	{
		DataSourceDomainUndeclared, "DATA_SOURCE_DOMAIN_UNDECLARED", SeverityError, "Data source on an undeclared domain",
		"An environment's base URL of a data source is not an HTTPS URL on a domain the plugin declares in capabilities.networkDomains, so every request would be blocked at run time (DAT-030, SEC-080).",
		"Declare the domain in the plugin's capabilities, or correct the environment's base URL.", false,
	},
	{
		DataMappingInvalid, "DATA_MAPPING_INVALID", SeverityError, "Invalid response mapping",
		"A data source's selector, response type or transform cannot produce its declared type: the selector is malformed, a transform is given without a response type, or the transform's type is not the declared one (DAT-004).",
		"Declare the response type the selector yields and a transform that returns the source's type, or select the declared type directly.", false,
	},
	{
		DataPaginationInvalid, "DATA_PAGINATION_INVALID", SeverityError, "Invalid pagination",
		"A paginated data source does not declare a list type, names a style other than cursor, page or offset, lacks the parameter or selector its style needs, or asks for pages larger than the limit data.pageSize (DAT-011).",
		"Declare a list type and the parameters of the style, and keep pageSize within data.pageSize.", false,
	},
	{
		DataSourceSecretHeader, "DATA_SOURCE_SECRET_HEADER", SeverityError, "Credential in a data source header",
		"A data source or operation sets a header that carries credentials (Authorization, Cookie, an API key or token). Secrets never appear in bundles; the auth delegate supplies the user's token and calls needing server-held secrets go through a Plux Function (DAT-003, SEC-107).",
		"Remove the header and set auth to true to send the auth delegate's token, or call the API through a Plux Function.", false,
	},
	{
		DataStreamInvalid, "DATA_STREAM_INVALID", SeverityError, "Invalid stream configuration",
		"A WebSocket, SSE or GraphQL subscription source is not what the data layer runs: a property is unknown or missing, the path is malformed, a subscription document is not a subscription, or a source of another kind is configured as a stream (DAT-012, ADR-0048).",
		"Correct the configuration as the data sources reference describes; the message names the property.", false,
	},
	{
		DataOutboxInvalid, "DATA_OUTBOX_INVALID", SeverityError, "Invalid offline mutation",
		"An operation marked offlineCapable cannot be replayed: it reads (GET, or a GraphQL query) instead of mutating, or it is also a file transfer (DAT-020).",
		"Mark only mutating operations offlineCapable, and treat their output as optional.", false,
	},
	{
		DataTransferInvalid, "DATA_TRANSFER_INVALID", SeverityError, "Invalid file transfer",
		"An operation's upload or download is not what the data layer runs: it is not a REST operation, its method does not fit the direction, its body mode or file parameter is unknown, or it is combined with offlineCapable (DAT-031).",
		"Declare a REST operation with a transfer of kind upload (POST, PUT or PATCH) or download (GET), and a file parameter.", false,
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
		"A navigate action passes a parameter value whose type differs from the parameter's declared type, or a deep-link pattern reads a parameter whose type a link's text cannot carry (a list, a map or an object).",
		"Pass a value of the declared type; read only scalar and enum parameters from a link's path.", false,
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
	{
		AnimationInvalid, "ANIMATION_INVALID", SeverityError, "Invalid animation timeline",
		"A timeline of a page is malformed: two timelines share a name, its keyframes are not in increasing time or run past the duration, a track has fewer keyframes than the limit anim.keyframesPerTrack allows or more, the duration exceeds anim.timelineDuration, or a driver names a node that does not exist (ANI-002, ANI-006).",
		"Name each timeline once, order keyframes by time within the duration, and keep them within the anim.* limits; the message names the timeline.", false,
	},
	{
		AnimationTargetInvalid, "ANIMATION_TARGET_INVALID", SeverityError, "Animation track targets nothing animatable",
		"A track names a node the page does not have, a prop the node's widget does not declare, or a prop whose type cannot be interpolated (only numbers, decimals and colours animate) (ANI-001, ANI-002).",
		"Target a node of the same page and a numeric or colour prop of its widget.", false,
	},
	{
		AnimationValueInvalid, "ANIMATION_VALUE_INVALID", SeverityError, "Keyframe value has the wrong type",
		"A keyframe's value is not a literal of the animated prop's type, or violates the prop's constraints (ANI-002).",
		"Write the keyframe as a literal of the prop's type.", false,
	},
	{
		TransitionInvalid, "TRANSITION_INVALID", SeverityError, "Invalid transition",
		"A custom route transition names no route timeline of the page, a route timeline animates a prop other than opacity, scale, slideX or slideY, or names a node, or a route timeline is used as anything but a transition (NAV-010).",
		"Declare a timeline with scope route in the page and name it in routeOptions.timeline.", false,
	},
	{
		AnimationExpensive, "ANIMATION_EXPENSIVE", SeverityWarning, "Animation that re-lays out the page every frame",
		"A timeline or an implicit animation changes a prop that affects layout (size, padding, margin, spacing, flex) of a node whose subtree is large, so every frame lays the subtree out again (ANI-008).",
		"Animate a transform (scale, slide) or opacity instead, which the compositor handles without layout.", false,
	},
	{
		AnimationOpacitySubtree, "ANIMATION_OPACITY_SUBTREE", SeverityWarning, "Opacity animated over a large subtree",
		"A timeline or an implicit animation changes the opacity of a node whose subtree has more nodes than the limit anim.compositedSubtree, which draws the subtree into an offscreen layer every frame (ANI-008).",
		"Fade the leaves instead, animate a smaller subtree, or use a transform.", false,
	},
	{
		AnimationTooManyTimelines, "ANIMATION_TOO_MANY_TIMELINES", SeverityWarning, "Many timelines can run together",
		"More timelines autoplay or run on a driver on one page than the limit anim.simultaneousTimelines, so they would all tick on every frame (ANI-008).",
		"Merge timelines, stagger their start, or start them from actions only when needed.", false,
	},
	{
		NodeAnimationInvalid, "NODE_ANIMATION_INVALID", SeverityError, "Invalid node animation",
		"A node's animation names a prop its widget does not declare or that cannot be interpolated, an empty hero tag, or an enter or exit transition on the page's root (ANI-001, ANI-003, ANI-004).",
		"Name animatable props of the widget, give the hero a non-empty tag, and put enter and exit transitions on inner nodes.", false,
	},

	// Schema and validation: capabilities and device actions.
	{
		CapabilityNotApproved, "CAPABILITY_NOT_APPROVED", SeverityError, "Capability not approved by the app",
		"A plugin declares a device API, network domain or function that the app document's capabilities do not approve. An app approves nothing it does not list (SEC-080, ADR-0051).",
		"Add the capability to the app document's capabilities if the app should allow it, or remove it from the plugin's capabilities.", false,
	},
	{
		DeviceCapabilityUndeclared, "DEVICE_CAPABILITY_UNDECLARED", SeverityError, "Device action without its capability",
		"A step runs a device action, such as pickImage or getLocation, or asks requestPermission for a permission, but the plugin does not declare the device API it needs in capabilities.deviceApis, so the runtime would block it (SEC-080).",
		"Declare the device API named in the message in the plugin's capabilities, or remove the step.", false,
	},
	{
		OpenURLDomainUndeclared, "OPEN_URL_DOMAIN_UNDECLARED", SeverityError, "openUrl on an undeclared domain",
		"An openUrl step opens an HTTPS URL on a domain the plugin does not declare in capabilities.networkDomains, or a URL that is not HTTPS and not one of the app's deep links, so the runtime would block it (SEC-080).",
		"Declare the domain in the plugin's capabilities, or open a deep link of the app instead.", false,
	},
	{
		HostBuildLacksPackage, "HOST_BUILD_LACKS_PACKAGE", SeverityWarning, "Host build lacks a Plux package",
		"The release uses a device action whose optional package, such as plux_media, plux_scanner or plux_location, is not among the packages that the native catalogue of one of the app's host builds records. On devices of that build the step fails with a permission error (RT-060, REL-080).",
		"Ship a host build that installs and registers the package and upload its catalogue with plux native sync, or keep the release from using the action; the publisher acknowledges the warning to publish anyway.", false,
	},

	// Schema and validation: form scopes.
	{
		FormScopeInvalid, "FORM_SCOPE_INVALID", SeverityError, "FormScope names no form",
		"A FormScope's form is not a literal naming a form that the page or component declares, so the form root below it would have no state to read (STA-020).",
		"Set form to the literal name of a form declared in the page's or component's forms.", false,
	},

	// Import: OpenAPI and GraphQL data sources.
	{
		ImportDocumentInvalid, "IMPORT_DOCUMENT_INVALID", SeverityError, "Document cannot be imported",
		"The OpenAPI document or GraphQL schema or operations file could not be read, parsed or validated, so nothing can be imported from it (DAT-002).",
		"Fix the reported problem in the source document and import again.", false,
	},
	{
		ImportConstructUnsupported, "IMPORT_CONSTRUCT_UNSUPPORTED", SeverityWarning, "Construct has no Plux type",
		"An operation uses a construct that no Plux type expresses, such as a polymorphic schema, a non-JSON body or a recursive type. The operation is left out of the import rather than typed loosely (DAT-002, SCH-010).",
		"Simplify the operation in the source document, or write the data source by hand.", false,
	},
	{
		ImportOperationInvalid, "IMPORT_OPERATION_INVALID", SeverityError, "Operation is invalid against its schema",
		"A GraphQL operation does not validate against the schema, or is not a single named query or mutation, so it cannot be imported as a typed operation (DAT-002).",
		"Correct the operation so it validates against the schema, and give it a name.", false,
	},
	{
		ImportOutputInvalid, "IMPORT_OUTPUT_INVALID", SeverityError, "Imported data sources fail validation",
		"The data sources produced by the import do not validate against the document schema, so they are not written (DAT-002, SCH-040).",
		"Report the source document that produced the failure; the importer must not emit a document the schema rejects.", false,
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
		PXLRegexInvalid, "PXL_REGEX_INVALID", SeverityError, "Invalid regular expression",
		"A pattern of `matches` is not in the pxl.regex.v1 subset of RE2: for example an unbalanced group, a lookaround, a backreference, a lazy quantifier or an unknown escape (schema/pxl/regex.md).",
		"Correct the pattern at the reported position; the PXL reference lists the supported syntax.", false,
	},
	{
		PXLRegexNotConstant, "PXL_REGEX_NOT_CONSTANT", SeverityError, "Regular expression is not a constant",
		"The pattern of `matches` is computed, so it cannot be compiled and checked at publish time.",
		"Write the pattern as a string literal.", false,
	},
	{
		PXLRegexTooLarge, "PXL_REGEX_TOO_LARGE", SeverityError, "Regular expression too large",
		"A pattern of `matches` exceeds the limits pxl.regexPatternLength, pxl.regexProgramSize or pxl.regexRepeat, which keep matching bounded on devices.",
		"Shorten the pattern or reduce its repetition counts; an installation may raise the limits within their maximums.", false,
	},
	{
		PXLUnknownPhoneRegion, "PXL_UNKNOWN_PHONE_REGION", SeverityError, "Unknown phone region",
		"The region of `isPhone` is a literal that is not a two-letter region with phone metadata (schema/pxl/phone.md).",
		"Use an ISO 3166 region code such as \"GB\", or pass the region from state or `device`.", false,
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
		AssetHashMismatch, "ASSET_HASH_MISMATCH", SeverityError, "Asset file hash mismatch",
		"An asset file's SHA-256 differs from the hash its signed bundle lists: the download is corrupt or has been tampered with (AST-001).",
		"Nothing to change in the app: the runtime discards the file and keeps the active release; sync again, and check the CDN or proxy if it persists.", false,
	},
	{
		SyncFailed, "SYNC_FAILED", SeverityError, "Sync failed",
		"The runtime could not complete a sync: the server was unreachable or answered with an error, a download failed after its retries, or a bundle failed verification after the full-bundle retry (SYN-010, SYN-011). The cause is in the error's details.",
		"The device keeps its current release and retries at the next start or manual sync. Check the server's health and the device's connectivity.", false,
	},

	// Runtime rendering and navigation.
	{
		NodeBuildFailed, "NODE_BUILD_FAILED", SeverityError, "Build error in node",
		"Building, laying out or painting a node failed, or a value it needs could not be decoded or evaluated. The page's error boundary rendered its fallback (RT-020). A PluxView that expands in unbounded constraints shows its fallback with it too (NAV-004).",
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
		"A step names an action, or a graph declares an option (a concurrency policy other than drop, a retry, a detached run), that this runtime does not run; the step fails with this error, which its onError can handle, and the option is ignored (ADR-0039). An exposed app state entry that declares a persistence is kept in memory until P5, and reported with it once in debug builds (ADR-0023).",
		"Raise the app's minimum runtime version to one that runs the action, or handle the error; the action reference page says which phase delivers each action.", false,
	},
	{
		PluginDisabled, "PLUGIN_DISABLED", SeverityWarning, "Plugin switched off",
		"The release's control switches turn the plugin off (kill switch), so every route into it shows its fallback page (RT-022).",
		"Turn the switch off in the release's channel controls once the problem is fixed.", false,
	},
	{
		RouteNotFound, "ROUTE_NOT_FOUND", SeverityError, "Route not found",
		"A navigation, a deep link or a PluxView names a route that is neither a page of the active release nor a registered native route, and for a PluxView no exported component has that key either. The app's not-found page is shown instead (NAV-011).",
		"Check the route name, publish the page that should answer it, or register the native route in PluxConfig.", false,
	},
	{
		RouteParametersInvalid, "ROUTE_PARAMETERS_INVALID", SeverityError, "Invalid route parameters",
		"A route was entered with a parameter missing, unknown or of the wrong type for the page's declaration. The page's error fallback is shown instead of the page (NAV-007).",
		"Pass every required parameter with the declared type; a deep link's or push payload's values must convert to the parameter types.", false,
	},
	{
		NavigationRefused, "NAVIGATION_REFUSED", SeverityWarning, "Navigation refused",
		"A route guard refused entry, because its graph decided on the fallback or failed and the guard failed closed (NAV-009); or the navigation needs what the screen does not have, such as a switchTab with no enclosing shell holding the tab, or a pop with nothing to pop (NAV-005).",
		"If the refusal is unexpected, check the guard's condition and the state it reads, such as the auth delegate, flags or the device's assurance level, or where the page is shown.", false,
	},
	{
		DeepLinkUnmapped, "DEEP_LINK_UNMAPPED", SeverityWarning, "Deep link not mapped",
		"A deep link or push payload names a host, scheme or path that the app's navigation.deepLinks do not map to a route, so nothing was opened (NAV-008).",
		"Add a path pattern for the link to navigation.deepLinks, or link to https://<host>/p/<route-name>, which always resolves.", false,
	},
	{
		NativeRouteNotRegistered, "NATIVE_ROUTE_NOT_REGISTERED", SeverityError, "Native route not registered",
		"A plugin navigated to a native route that the host app did not register in PluxConfig or expose through its router (NAV-002, HST-031).",
		"Register the route in the host app, or run plux native scan and sync so publishing checks the route against the host build (WGT-032).", false,
	},
	{
		NativeSlotNotRegistered, "NATIVE_SLOT_NOT_REGISTERED", SeverityError, "Native slot not registered",
		"A page places a native slot widget that the host app did not register in PluxConfig.nativeSlots. The page's error boundary shows a placeholder instead (WGT-033).",
		"Register the slot's builder in the host app, or run plux native scan and sync so publishing checks the slot against the host build (WGT-032).", false,
	},
	{
		NativeActionNotRegistered, "NATIVE_ACTION_NOT_REGISTERED", SeverityError, "Custom action not registered",
		"A callNative step names a custom action that the host app did not register in PluxConfig.nativeActions; the step fails and its onError handler runs (ACT-060).",
		"Register the action in the host app, or run plux native scan and sync so publishing checks the action against the host build (WGT-032).", false,
	},
	{
		ExposedStateTypeMismatch, "EXPOSED_STATE_TYPE_MISMATCH", SeverityError, "Exposed state written with the wrong type",
		"Native code wrote a value to an exposed state entry whose declared type the value does not have, read, wrote or watched a name the app does not expose, or wrote before a release was active; the write is refused and the entry keeps its value (STA-030, HST-021).",
		"Write a value of the entry's declared type; plux codegen generates typed accessors that make this a compile error.", false,
	},
	{
		UserContextInvalid, "USER_CONTEXT_INVALID", SeverityWarning, "User context attribute ignored",
		"Plux.setUserContext was given an attribute the app's userContext does not declare, or one whose text does not convert to its declared type; the attribute is left out of user.<name>, which reads as null (HST-011).",
		"Pass only the attributes the app declares, each as the text of its declared type: true or false, a number, a decimal, an ISO 8601 date, or an enum member.", false,
	},
	{
		HostCodeFailed, "HOST_CODE_FAILED", SeverityError, "Host code failed",
		"A custom action's handler, a native route's parameter conversion or screen, or a native slot's builder registered by the host app threw; the step fails, or the page shows its error fallback, and nothing reaches the plugin as a crash (ACT-060, NAV-002, WGT-033).",
		"Fix the host's handler, conversion or builder; the report names the action, route or slot and only the exception's type, never its message.", false,
	},
	{
		AnimationUnknown, "ANIMATION_UNKNOWN", SeverityError, "Unknown animation",
		"An action names a timeline the page does not own, or runs on a page without timelines (ANI-002).",
		"Name a timeline declared in the page's animations.", false,
	},
	{
		AnimationCommandInvalid, "ANIMATION_COMMAND_INVALID", SeverityError, "Invalid animation command",
		"controlAnimation was given a command it cannot run, such as seek without a position (ANI-002).",
		"Pass the position, in milliseconds, with seek.", false,
	},
	{
		AnimationTimelineBroken, "ANIMATION_TIMELINE_BROKEN", SeverityError, "Timeline cannot play",
		"A timeline of the bundle uses a curve, prop or value this runtime cannot play; the timeline does not run and the nodes keep their static values (ANI-002).",
		"Rebuild the bundle with a compiler of this runtime's generation.", false,
	},
	{
		AnimationAssetFailed, "ANIMATION_ASSET_FAILED", SeverityError, "Lottie or Rive asset failed",
		"A Lottie or Rive widget could not load or play its asset: the file is missing, malformed or not what the widget expects (ANI-005).",
		"Check the asset and its media type; the widget shows nothing in its place.", false,
	},
	{
		ActionTimeout, "ACTION_TIMEOUT", SeverityError, "Action timed out",
		"A step or its run took longer than the limits action.stepTimeout or action.runTimeout, or the step's own timeoutMs, allow; time spent waiting for the user in a dialog or bottom sheet does not count (ACT-005, ADR-0039).",
		"Handle the error with the step's onError, or make the work shorter; an installation may raise the limits within their maximums.", false,
	},
	{
		ActionStepLimitExceeded, "ACTION_STEP_LIMIT_EXCEEDED", SeverityError, "Step limit exceeded",
		"A run executed more steps than the limit action.stepsPerRun allows, and was stopped (ACT-005, ADR-0039).",
		"Shorten the graph, or move repeated work into a flow.", false,
	},
	{
		ActionValueInvalid, "ACTION_VALUE_INVALID", SeverityError, "Action value of the wrong type",
		"A step's input, output or result does not have the type its action, page or graph declares, or a binding of an input could not be evaluated; the step fails with a validation error (ADR-0039).",
		"Check the bindings of the step's inputs and the values the host passes or returns.", false,
	},
	{
		ActionCustomError, "ACTION_CUSTOM_ERROR", SeverityError, "Run failed with a custom error",
		"A stop step ended the run with a custom error code, and no onError handled it (ADR-0039).",
		"Handle the error with an onError edge, or check why the graph stops with it; the message carries its code.", false,
	},
	{
		ActionCancelled, "ACTION_CANCELLED", SeverityInfo, "Action run cancelled",
		"A run, or a branch of a parallel step, was cancelled: its page or component was disposed, a restart policy started a newer run, or another branch of the parallel step failed (ACT-003, ACT-004). Cancellation ends the run; it is never routed to an error handler.",
		"Nothing to fix when the owner went away. Mark the handler detached when its run must outlive its page.", false,
	},
	{
		ForEachLimitExceeded, "ACTION_FOREACH_LIMIT_EXCEEDED", SeverityError, "forEach item limit exceeded",
		"A forEach step was given more items than the limit action.forEachItems allows; the step fails before its body runs (ACT-005).",
		"Iterate over fewer items, for example a page of them, or raise the limit within its maximum.", false,
	},
	{
		FlowNotFound, "FLOW_NOT_FOUND", SeverityError, "Flow not found",
		"A callFlow step names a flow the active release does not hold, or a flow of another plugin that is not exported in that plugin's active version (ACT-061).",
		"Publish the plugin that declares the flow, export it, or handle the error with the step's onError.", false,
	},
	{
		ErrorHandlerFailed, "ERROR_HANDLER_FAILED", SeverityError, "Error handler failed",
		"A page, plugin or app error handler failed while handling a run's error; its failure is reported and the original error goes on to the next handler (ACT-020).",
		"Fix the error handler's graph; the report names the handler's owner and its own error code.", false,
	},
	{
		ActionQueueFull, "ACTION_QUEUE_FULL", SeverityWarning, "Action queue full",
		"A handler with the queue policy was triggered while it already held as many waiting triggers as the limit action.queueLength allows; the trigger was dropped (ACT-003).",
		"Use the drop, restart or debounce policy for triggers that come faster than their runs finish, or raise the limit within its maximum.", false,
	},
	{
		DataDomainBlocked, "DATA_DOMAIN_BLOCKED", SeverityError, "Request to an undeclared domain blocked",
		"A data source or operation was about to send a request to a host the plugin does not declare in capabilities.networkDomains, or over a scheme other than HTTPS. The request never left the device, and the attempt is reported (DAT-030, SEC-080).",
		"Declare the domain in the plugin's capabilities, or correct the base URL of the environment.", false,
	},
	{
		DataNetworkFailed, "DATA_NETWORK_FAILED", SeverityError, "Network request failed",
		"A data request could not be completed: the device is offline, the connection failed or was reset. The step's error has the kind network (DAT-001).",
		"Handle the error with onError, show the source's error state, or retry when the network returns.", false,
	},
	{
		DataHTTPError, "DATA_HTTP_ERROR", SeverityError, "Request answered with an error status",
		"The server answered a data request with a status outside 2xx. The step's error has the kind http and carries the status, never the response body (DAT-001, SCH-012).",
		"Handle the error with onError; check the request against the API.", false,
	},
	{
		DataRequestTimeout, "DATA_REQUEST_TIMEOUT", SeverityError, "Data request timed out",
		"A data request took longer than the limit data.requestTimeout. The step's error has the kind timeout (DAT-001).",
		"Handle the error with onError, or ask the API owner why it is slow; an installation may raise the limit within its maximum.", false,
	},
	{
		DataMappingFailed, "DATA_MAPPING_FAILED", SeverityError, "Response does not match its declared type",
		"A response is not JSON, its selector finds nothing, a value does not have the type the source or operation declares, or the transform failed. The step's error has the kind validation and names the path, never the value (DAT-004).",
		"Correct the selector, the declared types or the transform, or ask the API owner about the response.", false,
	},
	{
		DataSizeExceeded, "DATA_SIZE_EXCEEDED", SeverityError, "Request or response too large",
		"A data request's body is larger than data.requestSize, or its response larger than data.responseSize; the transfer is stopped (LIM-004).",
		"Request less data, for example with pagination, or raise the limit within its maximum.", false,
	},
	{
		DataGraphQLError, "DATA_GRAPHQL_ERROR", SeverityError, "GraphQL request failed",
		"A GraphQL response carried errors and no data. The step's error has the kind http; the GraphQL messages are not reported, since they may echo user data (DAT-001, SCH-012).",
		"Check the GraphQL document and its variables against the schema.", false,
	},
	{
		DataUnauthorised, "DATA_UNAUTHORISED", SeverityError, "Request unauthorised",
		"A data request that sends the auth delegate's token was answered with 401 again after one refresh, or no token was available. The step's error has the kind http (HST-010).",
		"Sign the user in again through the host app; check the auth delegate's refresh.", false,
	},
	{
		DataSourceUnavailable, "DATA_SOURCE_UNAVAILABLE", SeverityError, "Data source unavailable",
		"A step or binding names a data source or operation this release does not declare, the source has no base URL for the current environment, or its kind is one this runtime does not load yet (DAT-001, DAT-003).",
		"Declare the source and a base URL for every environment, or raise the app's minimum runtime version to one that loads its kind.", false,
	},
	{
		DataCacheUnavailable, "DATA_CACHE_UNAVAILABLE", SeverityWarning, "Response cache unavailable",
		"The response cache could not be read or written, or its encryption key could not be obtained; the request goes to the network as with networkOnly, and nothing is stored in the clear (DAT-010, LIM-004).",
		"Check the device's free storage and the key provider; the runtime keeps working without the cache.", false,
	},
	{
		DataStreamFailed, "DATA_STREAM_FAILED", SeverityError, "Stream failed",
		"A stream ended and will not reconnect: the server refused the connection for good (a client error), the stream's protocol was violated, or a GraphQL subscription reported an error. Network failures and server errors are not this error: they reconnect with backoff (DAT-012).",
		"Check the stream's URL, parameters and the user's access; subscribe again after correcting the cause.", false,
	},
	{
		DataStreamMessageTooLarge, "DATA_STREAM_MESSAGE_TOO_LARGE", SeverityError, "Stream message too large",
		"A message of a stream is larger than data.streamMessageSize; the stream is closed (DAT-012, LIM-004).",
		"Make the server send smaller messages, or raise data.streamMessageSize for the app.", false,
	},
	{
		DataStreamLimit, "DATA_STREAM_LIMIT", SeverityError, "Too many open streams",
		"A subscription would open more streams than data.streamsOpen allows; it is refused and the open streams stay (DAT-012, LIM-004).",
		"Unsubscribe from a stream first, or raise data.streamsOpen for the app.", false,
	},
	{
		DataOutboxFull, "DATA_OUTBOX_FULL", SeverityError, "Outbox full",
		"An offline mutation could not be queued because the outbox holds data.outboxEntries entries or data.outboxBytes bytes; the mutation is refused with a custom error and nothing is queued (DAT-020, LIM-004).",
		"Wait until connectivity returns and the outbox drains, or raise the limits for the app.", false,
	},
	{
		DataOutboxUnavailable, "DATA_OUTBOX_UNAVAILABLE", SeverityError, "Outbox unavailable",
		"The outbox could not be read or written, or its encryption key could not be obtained, so an offline mutation is refused rather than stored in the clear (DAT-020).",
		"Check the device's free storage and secure storage; the mutation can be repeated.", false,
	},
	{
		DataOutboxConflict, "DATA_OUTBOX_CONFLICT", SeverityWarning, "Outbox replay conflict",
		"A queued mutation was answered 409 or 412 when it was replayed: the server's state changed meanwhile. The entry is removed and the data source's conflict event runs, so the graph can reconcile (DAT-020).",
		"Handle the conflict trigger of the data source: reload the data and ask the user, or apply the change again.", false,
	},
	{
		DataOutboxRejected, "DATA_OUTBOX_REJECTED", SeverityWarning, "Outbox replay rejected",
		"A queued mutation was answered with a client error other than a conflict when it was replayed, so it can never succeed. The entry is removed and the data source's failure event runs (DAT-020).",
		"Handle the failure trigger of the data source: undo the optimistic change and tell the user.", false,
	},
	{
		DataTransferTooLarge, "DATA_TRANSFER_TOO_LARGE", SeverityError, "Transfer too large",
		"An upload's file, or a download's declared or received length, is larger than data.uploadSize or data.downloadSize; the transfer is refused or stopped and a partial download is removed (DAT-031, LIM-004).",
		"Choose a smaller file, or raise the limit for the app.", false,
	},
	{
		DataTransferFileFailed, "DATA_TRANSFER_FILE_FAILED", SeverityError, "Transfer file unavailable",
		"The file to upload does not exist or cannot be read, or the name a download is saved under is not a plain file name or cannot be written in the runtime's directory (DAT-031).",
		"Give an existing file, and a download a plain name without directories.", false,
	},
	{
		StateWriteTypeMismatch, "STATE_WRITE_TYPE_MISMATCH", SeverityError, "State written with a value of the wrong type",
		"A state write received a value from outside the bundle (an API response, a custom action, the host) that does not have the entry's declared type; the step fails with a validation error and the entry keeps its value (STA-002).",
		"Map the value to the declared type before writing it, or handle the step's error with onError.", false,
	},
	{
		StateWriteRefused, "STATE_WRITE_REFUSED", SeverityError, "State write refused",
		"A state write names a path its scope does not have or a computed entry, or patches an entry that is not an object; the step fails and nothing changes (STA-001, STA-004).",
		"Check the path against the entries the page, component, plugin, app or run declares; the compiler reports this for literal paths (PLX-1140, PLX-1141).", false,
	},
	{
		StateStoreUnavailable, "STATE_STORE_UNAVAILABLE", SeverityWarning, "State store unavailable",
		"The runtime could not open or write its local state store, or could not obtain the store's key from the platform's secure storage; session, persisted and secure entries are kept in memory until the store works again (STA-003).",
		"Check the device's free space and the platform's secure storage; the message names the failing operation, never a value.", false,
	},
	{
		StateStoreCorrupt, "STATE_STORE_CORRUPT", SeverityError, "State store failed authentication",
		"The local state store did not decrypt under its key: it was altered, truncated or written by another installation. It is discarded, and every stored entry starts from its default (STA-003).",
		"Nothing to fix in the app; if it repeats, check the device for tampering with the app's storage.", false,
	},
	{
		StateMigrationFailed, "STATE_MIGRATION_FAILED", SeverityWarning, "Stored state could not be migrated",
		"A stored value had neither the entry's type nor the type its migration reads, or the migration failed; the entry starts from its default (STA-040).",
		"Declare a migration from the previous type, and check that it handles every stored value.", false,
	},
	{
		StateLimitExceeded, "STATE_LIMIT_EXCEEDED", SeverityError, "Stored state over its limit",
		"The values to store exceed state.persistedBytes or state.secureBytes; the write takes effect in memory but is not stored, so it is lost when the app is closed (LIM-001, LIM-004).",
		"Store less, for example by keeping large data in a local collection, or raise the limit for the app.", false,
	},
	{
		HostEventRefused, "HOST_EVENT_REFUSED", SeverityError, "Host event refused",
		"Plux.sendEvent named an event the app does not declare for the host to send (direction toPlux or both), or a payload without the event's fields and types; nothing runs (HST-013).",
		"Declare the event in the app's hostEvents with the direction toPlux or both; plux codegen generates typed senders that make this a compile error.", false,
	},
	{
		FormInvalid, "FORM_INVALID", SeverityError, "Form invalid",
		"submitForm validated a form and at least one field is invalid; the step fails with a validation error naming the fields, every field is marked touched, and the field errors are in the form's state (STA-020).",
		"Show the form's errors, and handle the step's error with onError where the run should go on.", false,
	},
	{
		FormNotInScope, "FORM_NOT_IN_SCOPE", SeverityError, "Form not in scope",
		"A form action named a form that neither the component nor the page where the run started declares, for example from a lifecycle run that has no form state (STA-020).",
		"Run the form action from a handler of the page or component that declares the form.", false,
	},
	{
		FormAsyncValidatorFailed, "FORM_ASYNC_VALIDATOR_FAILED", SeverityWarning, "Asynchronous validator failed",
		"An asynchronous validator's graph failed, for example because the server could not be reached; the field is shown as not checked and the form is invalid until a check succeeds (STA-020).",
		"Handle the graph's errors with onError and return a message, or let the user retry by editing the field.", false,
	},

	// Actions: device and feedback.
	{
		DeviceCapabilityBlocked, "DEVICE_CAPABILITY_BLOCKED", SeverityError, "Device operation blocked",
		"A step ran a device action whose device API the plugin does not declare, or that the host narrowed away with PluxConfig.allowedCapabilities. The step failed with a permission error and nothing reached the device (SEC-080).",
		"Declare the device API in the plugin's capabilities and in the app's approved set, or allow it in the host's allowedCapabilities.", false,
	},
	{
		DevicePackageMissing, "DEVICE_PACKAGE_MISSING", SeverityError, "Device package not installed",
		"A step ran a device action whose optional package, such as plux_media, plux_scanner or plux_location, the host app did not register in PluxConfig.devicePackages (RT-060).",
		"Add the package to the host app and pass it in PluxConfig.devicePackages.", false,
	},
	{
		DevicePermissionDenied, "DEVICE_PERMISSION_DENIED", SeverityError, "Device permission denied",
		"The user, or the platform's policy, denied the permission a device action needs, such as the camera or location.",
		"Run requestPermission first and take its denied branch to explain what the feature needs; the user can grant the permission in the system settings.", false,
	},
	{
		DeviceUnavailable, "DEVICE_UNAVAILABLE", SeverityError, "Device feature unavailable",
		"The platform could not perform the device operation: the device has no such hardware, the service is switched off, or the platform call failed.",
		"Check that the device supports the feature and that its service, such as location, is on.", false,
	},
	{
		OpenURLBlocked, "OPEN_URL_BLOCKED", SeverityError, "openUrl blocked",
		"An openUrl step named a URL that is not HTTPS on a domain the plugin declares, nor a deep link of the app, or the platform could not open it (SEC-080).",
		"Declare the domain in the plugin's capabilities, or use a deep link of the app.", false,
	},
	{
		ClipboardBlocked, "CLIPBOARD_BLOCKED", SeverityError, "Clipboard write blocked",
		"A copyToClipboard step ran on a page marked secure, or copied more text than device.clipboardChars allows, so nothing was copied (SEC-090).",
		"Do not copy from secure pages, or shorten the text.", false,
	},

	// Actions: typed host events.
	{
		HostEventPayloadInvalid, "HOST_EVENT_PAYLOAD_INVALID", SeverityError, "Host event payload invalid",
		"The payload of Plux.sendEvent lacks a field the app's hostEvents declaration requires, has a field it does not declare, or has a value that does not fit the field's type; nothing runs (HST-013).",
		"Send the declared fields with values of their types; plux codegen generates typed senders that make this a compile error.", false,
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
		AssetsNotReady, "ASSETS_NOT_READY", SeverityError, "Assets still being processed",
		"A publish waits until every image asset of the app has its variants (CMP-030), since a bundle compiled without them would differ from the release's (REL-003). Some were still being processed when the wait, publish.assetWait, ran out: the worker's asset jobs are slow, failing or not running.",
		"Check the assets' processing state, and the worker's asset jobs if an asset stays pending; publish again once every asset is ready.", false,
	},
	{
		HostBuildIncompatible, "HOST_BUILD_INCOMPATIBLE", SeverityWarning, "Host build lacks a native entry",
		"The release uses a native route, native slot or custom action that the native catalogue of one of the app's host builds lacks, or declares with other parameters, props, events, inputs or result types. Devices of that build keep receiving the newest release compatible with it (WGT-032, REL-080).",
		"Ship a host build that registers the entry and upload its catalogue with plux native sync, or keep the release from using the entry; the publisher acknowledges the warning to publish anyway.", false,
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
