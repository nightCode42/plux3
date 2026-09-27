// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	flatbuffers "github.com/google/flatbuffers/go"

	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// strings creates a vector of strings.
func stringVector(b *flatbuffers.Builder, items []string, start func(*flatbuffers.Builder, int) flatbuffers.UOffsetT) flatbuffers.UOffsetT {
	offs := make([]flatbuffers.UOffsetT, len(items))
	for i, s := range items {
		offs[i] = b.CreateString(s)
	}
	return offsetVector(b, offs, start)
}

// offsetVector creates a vector of offsets.
func offsetVector(b *flatbuffers.Builder, offs []flatbuffers.UOffsetT, start func(*flatbuffers.Builder, int) flatbuffers.UOffsetT) flatbuffers.UOffsetT {
	start(b, len(offs))
	for i := len(offs) - 1; i >= 0; i-- {
		b.PrependUOffsetT(offs[i])
	}
	return b.EndVector(len(offs))
}

// metaSection builds a meta section.
func metaSection(kind fbs.BundleKind, features ...string) []byte {
	b := flatbuffers.NewBuilder(0)
	feats := stringVector(b, features, fbs.MetaStartRequiredFeaturesVector)
	key := b.CreateString("loans")
	fbs.MetaStart(b)
	fbs.MetaAddKind(b, kind)
	fbs.MetaAddId(b, fbs.CreateUuid(b, 1, 2))
	fbs.MetaAddKey(b, key)
	fbs.MetaAddRequiredFeatures(b, feats)
	Finish(b, fbs.MetaEnd(b), SectionMeta)
	return b.FinishedBytes()
}

// value builds a value of every shape: a list holding a decimal, a map
// entry and a nested object.
func value(b *flatbuffers.Builder) flatbuffers.UOffsetT {
	unscaled := b.CreateByteVector([]byte{0x04, 0xd2})
	fbs.ValueStart(b)
	fbs.ValueAddKind(b, fbs.ValueKindDecimal)
	fbs.ValueAddUnscaled(b, unscaled)
	fbs.ValueAddScale(b, 2)
	dec := fbs.ValueEnd(b)
	fbs.ValueStart(b)
	fbs.ValueAddKind(b, fbs.ValueKindString)
	fbs.ValueAddS(b, 1)
	str := fbs.ValueEnd(b)
	fbs.EntryStart(b)
	fbs.EntryAddKey(b, 2)
	fbs.EntryAddValue(b, str)
	entry := fbs.EntryEnd(b)
	entries := offsetVector(b, []flatbuffers.UOffsetT{entry}, fbs.ValueStartEntriesVector)
	fbs.ValueStart(b)
	fbs.ValueAddKind(b, fbs.ValueKindMap)
	fbs.ValueAddEntries(b, entries)
	m := fbs.ValueEnd(b)
	items := offsetVector(b, []flatbuffers.UOffsetT{dec, m}, fbs.ValueStartItemsVector)
	fbs.ValueStart(b)
	fbs.ValueAddKind(b, fbs.ValueKindList)
	fbs.ValueAddItems(b, items)
	return fbs.ValueEnd(b)
}

// pageSection builds a page with two nodes, props, a handler, a slot, an
// override and its string table.
func pageSection() []byte {
	b := flatbuffers.NewBuilder(0)
	strs := stringVector(b, []string{"", "Loan", "amount", "medium", "home"}, fbs.PageStartStringsVector)
	var props []flatbuffers.UOffsetT
	for id := range uint32(2) {
		v := value(b)
		fbs.PropStart(b)
		fbs.PropAddId(b, id+1)
		fbs.PropAddValue(b, v)
		props = append(props, fbs.PropEnd(b))
	}
	propVec := offsetVector(b, props, fbs.NodeStartPropsVector)
	overrideProps := offsetVector(b, props[:1], fbs.OverrideStartPropsVector)
	fbs.OverrideStart(b)
	fbs.OverrideAddKind(b, fbs.OverrideKindSizeClass)
	fbs.OverrideAddKey(b, 3)
	fbs.OverrideAddProps(b, overrideProps)
	override := offsetVector(b, []flatbuffers.UOffsetT{fbs.OverrideEnd(b)}, fbs.NodeStartOverridesVector)
	fbs.HandlerStart(b)
	fbs.HandlerAddEvent(b, 1)
	fbs.HandlerAddGraph(b, fbs.CreateUuid(b, 3, 4))
	fbs.HandlerAddConcurrency(b, fbs.ConcurrencyDebounce)
	fbs.HandlerAddIntervalMs(b, 300)
	handlers := offsetVector(b, []flatbuffers.UOffsetT{fbs.HandlerEnd(b)}, fbs.NodeStartHandlersVector)
	fbs.SlotFillStartNodesVector(b, 1)
	b.PrependUint32(1)
	slotNodes := b.EndVector(1)
	fbs.SlotFillStart(b)
	fbs.SlotFillAddId(b, 1)
	fbs.SlotFillAddNodes(b, slotNodes)
	slots := offsetVector(b, []flatbuffers.UOffsetT{fbs.SlotFillEnd(b)}, fbs.NodeStartSlotsVector)
	fbs.NodeStart(b)
	fbs.NodeAddId(b, fbs.CreateUuid(b, 5, 6))
	fbs.NodeAddWidget(b, 7)
	fbs.NodeAddProps(b, propVec)
	fbs.NodeAddHandlers(b, handlers)
	fbs.NodeAddSlots(b, slots)
	fbs.NodeAddOverrides(b, override)
	fbs.NodeAddHints(b, byte(fbs.NodeHintsRepaintBoundary))
	root := fbs.NodeEnd(b)
	fbs.NodeStart(b)
	fbs.NodeAddWidget(b, 8)
	fbs.NodeAddTestId(b, 4)
	leaf := fbs.NodeEnd(b)
	nodes := offsetVector(b, []flatbuffers.UOffsetT{root, leaf}, fbs.PageStartNodesVector)
	title := value(b)
	fbs.PageStart(b)
	fbs.PageAddId(b, fbs.CreateUuid(b, 9, 10))
	fbs.PageAddKey(b, 4)
	fbs.PageAddTitle(b, title)
	fbs.PageAddNodes(b, nodes)
	fbs.PageAddStrings(b, strs)
	Finish(b, fbs.PageEnd(b), SectionPage)
	return b.FinishedBytes()
}

// stringsSection builds a strings section.
func stringsSection(items ...string) []byte {
	b := flatbuffers.NewBuilder(0)
	v := stringVector(b, items, fbs.StringsStartStringsVector)
	fbs.StringsStart(b)
	fbs.StringsAddStrings(b, v)
	Finish(b, fbs.StringsEnd(b), SectionStrings)
	return b.FinishedBytes()
}

// sampleSections is a plugin bundle's sections.
func sampleSections() []Section {
	return []Section{
		{Kind: SectionStrings, ID: ID{1}, Data: stringsSection("", "a", "ü")},
		{Kind: SectionPage, ID: ID{2}, Data: pageSection()},
		{Kind: SectionPage, ID: ID{1}, Data: pageSection()},
		{Kind: SectionMeta, ID: ID{1}, Data: metaSection(fbs.BundleKindPlugin, "pxl.v1")},
	}
}

// supportsAll accepts every feature.
func supportsAll(string) bool { return true }

// defaultOptions reads with the registry defaults.
func defaultOptions() ReadOptions {
	return ReadOptions{Limits: limits.Defaults(), Supports: supportsAll}
}
