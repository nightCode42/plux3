// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The server's SVG compiler (CMP-031, ADR-0027 Revision): one SVG in, its
/// `vector_graphics` encoding out, with the masking, clipping and overdraw
/// optimisers on when Skia's path operations are loaded.
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:vector_graphics_compiler/vector_graphics_compiler.dart';

/// The usage line.
const usage =
    'usage: plux-svgc (--libpathops <path> | --no-path-ops) < in.svg > out.vec';

/// Encodes [svg], with the optimisers when [optimise] (they need Skia's
/// path operations, `initializeLibPathOps`). The output depends on [svg]
/// and [optimise] alone.
Uint8List compileSvg(String svg, {required bool optimise}) => encodeSvg(
  xml: svg,
  debugName: 'svg',
  enableMaskingOptimizer: optimise,
  enableClippingOptimizer: optimise,
  enableOverdrawOptimizer: optimise,
);

/// Runs the tool: [args] name the path operations library, or opt out of
/// the optimisers (for development only); [input] is the SVG, UTF-8; the encoding goes to
/// [output] and every message to [errors]. Returns the exit code: 0, 1 for
/// an SVG that cannot be compiled, 2 for a usage error.
Future<int> run(
  List<String> args,
  Stream<List<int>> input,
  IOSink output,
  IOSink errors,
) async {
  final bool optimise;
  switch (args) {
    case ['--libpathops', final path]:
      if (!File(path).existsSync()) {
        errors.writeln('plux-svgc: no path operations library at $path');
        return 2;
      }
      initializeLibPathOps(path);
      optimise = true;
    case ['--no-path-ops']:
      optimise = false;
    default:
      errors.writeln(usage);
      return 2;
  }
  final String svg;
  try {
    svg = await utf8.decoder.bind(input).join();
  } on FormatException catch (e) {
    errors.writeln('plux-svgc: the SVG is not UTF-8: ${e.message}');
    return 1;
  }
  // The encoder may print; only the encoding goes to the output.
  final Uint8List encoded;
  try {
    encoded = runZoned(
      () => compileSvg(svg, optimise: optimise),
      zoneSpecification: ZoneSpecification(
        print: (_, _, _, line) => errors.writeln(line),
      ),
    );
  } on Object catch (e) {
    errors.writeln('plux-svgc: the SVG cannot be compiled: $e');
    return 1;
  }
  output.add(encoded);
  return 0;
}
