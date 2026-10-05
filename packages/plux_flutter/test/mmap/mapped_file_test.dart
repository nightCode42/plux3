// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/mmap/mapped_file.dart';

void main() {
  late Directory dir;
  setUp(() => dir = Directory.systemTemp.createTempSync('plux_mmap'));
  tearDown(() => dir.deleteSync(recursive: true));

  test('maps a file zero-copy and releases it [RT-010]', () {
    final f = File('${dir.path}/b.pxb')
      ..writeAsBytesSync(List<int>.generate(10000, (i) => i % 251));
    final m = MappedFile.open(f.path);
    expect(m.bytes.length, 10000);
    expect(m.bytes[250], 250);
    expect(m.bytes[251], 0);
    expect(m.bytes, isA<Uint8List>());
    expect(m.isReleased, isFalse);
    m.release();
    expect(m.isReleased, isTrue);
    expect(() => m.bytes, throwsStateError);
    m.release(); // idempotent
  });

  test('a view taken before release stays readable: the mapping lives as long as a view [RT-010]', () {
    final f = File('${dir.path}/kept.pxb')
      ..writeAsBytesSync(List<int>.generate(4096, (i) => i % 251));
    final m = MappedFile.open(f.path);
    // A decoded bundle object keeps a view like this one; a replaced
    // release gives its file up while such objects may still be read.
    final view = Uint8List.sublistView(m.bytes, 100, 200);
    m.release();
    expect(() => m.bytes, throwsStateError);
    expect(view[0], 100);
    expect(view[99], 199 % 251);
  });

  test('maps an empty file as no bytes', () {
    final f = File('${dir.path}/empty')..writeAsBytesSync([]);
    final m = MappedFile.open(f.path);
    expect(m.bytes, isEmpty);
    m.release();
  });

  test('reports a missing file and a directory', () {
    expect(
      () => MappedFile.open('${dir.path}/missing'),
      throwsA(isA<FileSystemException>()),
    );
    expect(
      () => MappedFile.open(dir.path),
      throwsA(isA<FileSystemException>()),
    );
  });

  test('a mapping made after a rename still reads the file it named', () {
    final f = File('${dir.path}/a')..writeAsBytesSync([1, 2, 3]);
    final m = MappedFile.open(f.path);
    File('${dir.path}/b').writeAsBytesSync([9, 9, 9]);
    File('${dir.path}/b').renameSync(f.path);
    expect(m.bytes, [1, 2, 3], reason: 'the store replaces files by rename');
    m.release();
  });
}
