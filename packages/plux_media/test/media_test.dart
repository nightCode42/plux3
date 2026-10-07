// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:file_picker/file_picker.dart' show FileType;
import 'package:flutter/services.dart' show PlatformException;
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_media/plux_media.dart';
import 'package:plux_media/src/media.dart'
    show mimeAccepted, mimeForName, pickerFilter;

final class FakeImages implements ImageBackend {
  List<PickedEntry> picked = [];
  PickedEntry? captured;
  PlatformException? failure;
  final List<String> calls = [];

  @override
  Future<List<PickedEntry>> pick({
    required bool multiple,
    required int limit,
    int? maxDimension,
  }) async {
    calls.add('pick $multiple $limit $maxDimension');
    if (failure case final f?) throw f;
    return picked;
  }

  @override
  Future<PickedEntry?> capture({int? maxDimension}) async {
    calls.add('capture $maxDimension');
    if (failure case final f?) throw f;
    return captured;
  }
}

final class FakeFiles implements FileBackend {
  List<PickedEntry> picked = [];
  final List<String> calls = [];

  @override
  Future<List<PickedEntry>> pick({
    required FileType type,
    required List<String> extensions,
    required bool multiple,
  }) async {
    calls.add('${type.name} ${extensions.join(',')} $multiple');
    return picked;
  }
}

PickedEntry entry(String name, {String? mime, int size = 7}) =>
    (path: '/cache/$name', name: name, mimeType: mime, size: size);

void main() {
  late FakeImages images;
  late FakeFiles files;
  late PluxMediaPicker picker;

  setUp(() {
    images = FakeImages();
    files = FakeFiles();
    final media = PluxMedia(images: images, files: files);
    picker = media.services[PluxMediaPicker]! as PluxMediaPicker;
  });

  test(
    'the package registers itself as plux_media and provides the picker',
    () {
      final media = PluxMedia(images: images, files: files);
      expect(media, isA<PluxDevicePackage>());
      expect(media.name, 'plux_media');
      expect(media.services.keys, [PluxMediaPicker]);
    },
  );

  test('pickImages passes the limit and size, and keeps at most the limit [RT-060]', () async {
    images.picked = [
      entry('a.jpg', mime: 'image/jpeg'),
      entry('b.png'),
      entry('c.heic'),
    ];
    final got = await picker.pickImages(
      multiple: true,
      limit: 2,
      maxDimension: 800,
    );
    expect(images.calls.single, 'pick true 2 800');
    expect(got.map((f) => f.name), ['a.jpg', 'b.png']);
    expect(got[0].mimeType, 'image/jpeg');
    expect(got[1].mimeType, 'image/png');
    expect(got[0].sizeBytes, 7);
    expect(got[0].path, '/cache/a.jpg');
  });

  test('a cancelled image pick or capture is empty or null', () async {
    expect(await picker.pickImages(multiple: false, limit: 1), isEmpty);
    expect(await picker.capturePhoto(), isNull);
  });

  test('capturePhoto returns the photo, JPEG by default', () async {
    images.captured = entry('IMG_1');
    final photo = await picker.capturePhoto(maxDimension: 1024);
    expect(images.calls.single, 'capture 1024');
    expect(photo?.mimeType, 'image/jpeg');
    expect(photo?.name, 'IMG_1');
  });

  test(
    'pickFiles filters by the accepted types and limits the count',
    () async {
      files.picked = [
        entry('a.pdf'),
        entry('b.png'),
        entry('c.pdf'),
        entry('d.pdf'),
      ];
      final got = await picker.pickFiles(
        mimeTypes: ['application/pdf'],
        multiple: true,
        limit: 2,
      );
      expect(files.calls.single, 'custom pdf true');
      expect(got.map((f) => f.name), ['a.pdf', 'c.pdf']);
      expect(got.first.mimeType, 'application/pdf');
    },
  );

  test('pickFiles without types accepts any file', () async {
    files.picked = [entry('notes.unknown')];
    final got = await picker.pickFiles(
      mimeTypes: const [],
      multiple: false,
      limit: 1,
    );
    expect(files.calls.single, 'any  false');
    expect(got.single.mimeType, 'application/octet-stream');
  });

  test(
    'denied permissions and platform failures are typed, without paths',
    () async {
      images.failure = PlatformException(
        code: 'camera_access_denied',
        message: '/private/path',
      );
      await expectLater(
        picker.capturePhoto(),
        throwsA(
          isA<PluxDeviceException>()
              .having((e) => e.failure, 'failure', PluxDeviceFailure.denied)
              .having((e) => e.message, 'message', isNot(contains('/private'))),
        ),
      );
      images.failure = PlatformException(code: 'already_active');
      await expectLater(
        picker.pickImages(multiple: false, limit: 1),
        throwsA(
          isA<PluxDeviceException>().having(
            (e) => e.failure,
            'failure',
            PluxDeviceFailure.unavailable,
          ),
        ),
      );
    },
  );

  group('filters', () {
    void filter(List<String> mimes, FileType type, List<String> extensions) {
      final got = pickerFilter(mimes);
      expect(got.$1, type, reason: '$mimes');
      expect(got.$2, extensions, reason: '$mimes');
    }

    test('wildcards map to the platform file types', () {
      filter(['image/*'], FileType.image, []);
      filter(['video/*'], FileType.video, []);
      filter(['audio/*'], FileType.audio, []);
      filter(['image/*', 'video/*'], FileType.media, []);
      filter(['text/*'], FileType.any, []);
      filter([], FileType.any, []);
    });

    test('known types map to extensions, unknown types open any file', () {
      filter(['image/jpeg'], FileType.custom, ['jpeg', 'jpg']);
      filter(['application/pdf', 'text/csv'], FileType.custom, ['csv', 'pdf']);
      filter(['application/x-unknown'], FileType.any, []);
    });

    test('media types are matched exactly or by wildcard', () {
      expect(mimeAccepted('image/png', ['image/*']), isTrue);
      expect(mimeAccepted('image/png', ['image/png']), isTrue);
      expect(mimeAccepted('image/png', ['application/pdf']), isFalse);
      expect(mimeAccepted('imagefoo/png', ['image/*']), isFalse);
      expect(mimeForName('A.PDF'), 'application/pdf');
      expect(mimeForName('noext'), isNull);
      expect(mimeForName('trailing.'), isNull);
    });
  });
}
