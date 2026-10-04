<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# `pxl.phone.v1` — phone-number validation by region

The specification of `isPhone(s, region)` (`PXL-006`, `STA-020`), implemented by Go
(`backend/internal/pxl/phone`) and Dart (`packages/plux_flutter/lib/src/pxl/phone.dart`);
`schema/testdata/phone/examples.json` and the PXL vectors prove they agree. Formatting
(`format.phone`) belongs to the `format` group (P8).

## 1. Metadata

`phone.json` is derived, never edited: `make phone-metadata` downloads libphonenumber's
`resources/PhoneNumberMetadata.xml` at the pinned release (Apache-2.0, The Libphonenumber
Authors) and `tools/cmd/phonemeta` keeps, per territory, the calling code, whether it is the
main territory of its code, the leading digits, the international and national prefixes, the
national-prefix transform rule, and each number type's pattern, lengths and example —
resolved as libphonenumber's `BuildMetadataFromXml` resolves them (a type without lengths
takes the general description's; the general lengths are the union of the types', excluding
`noInternationalDialling`; local-only lengths are kept for the general description only).
The source's SHA-256 is recorded.

## 2. Table

`make gen` writes one text table, read identically by both runtimes
(`backend/internal/pxl/phone/table_gen.go`, `lib/src/pxl/phone.g.dart`): one line per
territory, fields separated by one space, `~` for an empty field:

`id code main leadingDigits internationalPrefix nationalPrefixForParsing transformRule generalPattern generalLengths localOnlyLengths { lengths pattern }`

Lengths are comma-separated; types with equal lengths are joined into one alternation.
Patterns run on the `pxl.regex.v1` engine (every one compiles; the tests prove it) and are
compiled on first use.

## 3. Algorithm

libphonenumber's `parse(s, region)` followed by `isValidNumber`, on a stricter input:

1. More than 250 code points: invalid. Remove the separators space, `-`, `.`, `(`, `)`, `/`
   and U+00A0; what remains must be one optional leading `+` and at least one ASCII digit.
   Letters, extensions and other characters make the number invalid.
2. The region is two ASCII letters in either case with metadata; otherwise there is none.
3. **Calling code** (`maybeExtractCountryCode`). With `+`: the digits must be more than two,
   not start with 0, and start with a known calling code of one to three digits. Without `+`
   and with a region: if the region's international prefix matches at the start (leftmost-first)
   and the next digit is not 0, it is removed and the rest is read as after `+`; otherwise, if
   the digits start with the region's calling code and either removing it (and the national
   prefix, step 5) turns a number that does not match the general pattern into one that does,
   or the digits are too long for the region (step 6), the code is removed. Otherwise the
   region's code applies to all the digits. Without `+` and without a region: invalid.
4. A national number shorter than 2 digits is invalid.
5. **National prefix** (`maybeStripNationalPrefixAndCarrierCode`), with the metadata of the
   code's main territory when the code came from the number, else of the region: if the
   national-prefix pattern matches at the start, the candidate is the rest — preceded by the
   transform rule with `$1`–`$9` replaced by the groups, when there is a rule and the last
   group took part. The candidate is kept unless the number matched the general pattern and
   the candidate does not; it then replaces the number only if its length test (step 6) is
   *possible* or *too long*.
6. **Length test** against the general lengths L and local-only lengths LL: in LL → local only;
   the minimum of L → possible; below it → too short; above the maximum → too long; in L →
   possible; else invalid.
7. A national number outside 2–17 digits is invalid.
8. **Territory** (`getRegionCodeForNumber`): the code's only territory; or, in order (main
   first, then file order), the first whose leading-digits pattern matches at the start or,
   without one, for which the number is valid (step 9). None: invalid.
9. **Valid** (`getNumberTypeHelper` ≠ unknown): the number fully matches the general pattern
   with a general length, and fully matches some type's pattern with one of that type's
   lengths.

Known differences from libphonenumber: no letters (vanity numbers), extensions, `tel:` URIs,
full-width digits or Unicode punctuation, and a `+` followed by an international prefix is
invalid.
