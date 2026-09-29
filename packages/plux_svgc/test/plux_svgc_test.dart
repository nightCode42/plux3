// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:plux_svgc/plux_svgc.dart';
import 'package:test/test.dart';
import 'package:vector_graphics_codec/vector_graphics_codec.dart';

/// An icon with a clip path and a mask, which the optimisers resolve.
const icon = '''
<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">
  <defs>
    <clipPath id="c"><circle cx="12" cy="12" r="10"/></clipPath>
    <mask id="m"><rect width="24" height="24" fill="white"/><circle cx="12" cy="12" r="4" fill="black"/></mask>
  </defs>
  <g clip-path="url(#c)" mask="url(#m)">
    <rect width="24" height="24" fill="#6750A4"/>
    <rect x="4" y="4" width="16" height="16" fill="#FF5722"/>
  </g>
</svg>
''';

/// Runs the tool on [input] and returns its exit code, output and errors.
Future<(int, Uint8List, String)> svgc(
  List<String> args,
  List<int> input,
) async {
  final dir = Directory.systemTemp.createTempSync('svgc');
  addTearDown(() => dir.deleteSync(recursive: true));
  final out = File('${dir.path}/out').openWrite();
  final err = File('${dir.path}/err').openWrite();
  final code = await run(args, Stream.value(input), out, err);
  await out.close();
  await err.close();
  return (
    code,
    File('${dir.path}/out').readAsBytesSync(),
    File('${dir.path}/err').readAsStringSync(),
  );
}

/// Whether [encoded] is a complete vector_graphics encoding.
bool decodes(Uint8List encoded) => const VectorGraphicsCodec()
    .decode(ByteData.sublistView(encoded), null)
    .complete;

void main() {
  test('an SVG is compiled to vector_graphics, the same bytes every time '
      '[CMP-031]', () async {
    final (code, out, err) = await svgc(['--no-path-ops'], utf8.encode(icon));
    expect((code, err), (0, ''));
    expect(decodes(out), isTrue);
    final (_, again, _) = await svgc(['--no-path-ops'], utf8.encode(icon));
    expect(again, out);
  });

  test('the widget gallery\'s SVG compiles to the golden variant the '
      'runtime tests draw [CMP-031]', () async {
    const fixture = '../../schema/testdata/documents/widgets/assets/icons';
    final (code, out, _) = await svgc([
      '--no-path-ops',
    ], File('$fixture/check.svg').readAsBytesSync());
    expect(code, 0);
    expect(
      out,
      File('../../schema/testdata/bundles/widgets/variants/check.vec')
          .readAsBytesSync(),
    );
  });

  // The server image builds libpath_ops and runs this check; locally, point
  // PLUX_PATH_OPS at a build of it.
  final pathOps = Platform.environment['PLUX_PATH_OPS'];
  test('with Skia path operations the optimisers resolve clips and masks '
      '[CMP-031]', () async {
    final (code, out, err) = await svgc([
      '--libpathops',
      pathOps!,
    ], utf8.encode(icon));
    expect((code, err), (0, ''));
    expect(decodes(out), isTrue);
    final (_, plain, _) = await svgc(['--no-path-ops'], utf8.encode(icon));
    expect(out, isNot(plain));
  }, skip: pathOps == null ? 'PLUX_PATH_OPS is not set' : false);

  test(
    'a file that is not an SVG, or not UTF-8, fails with a message',
    () async {
      for (final (input, message) in [
        (utf8.encode('<svg'), 'cannot be compiled'),
        (<int>[0xff, 0xfe, 0x00], 'not UTF-8'),
      ]) {
        final (code, out, err) = await svgc(['--no-path-ops'], input);
        expect(code, 1);
        expect(out, isEmpty);
        expect(err, contains(message));
      }
    },
  );

  test('bad arguments are a usage error', () async {
    for (final args in [
      <String>[],
      ['--libpathops'],
      ['--libpathops', '/no/such/libpath_ops.so'],
      ['--optimise'],
    ]) {
      final (code, out, err) = await svgc(args, utf8.encode(icon));
      expect(code, 2, reason: '$args');
      expect(out, isEmpty);
      expect(err, isNotEmpty);
    }
  });
}
