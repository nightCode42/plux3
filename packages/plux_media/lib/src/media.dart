// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:file_picker/file_picker.dart';
import 'package:flutter/foundation.dart' show visibleForTesting;
import 'package:flutter/services.dart' show PlatformException;
import 'package:image_picker/image_picker.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// A file a backend picked, before the runtime keeps it behind a handle.
typedef PickedEntry = ({String path, String name, String? mimeType, int size});

/// Picks and captures images. The default is `image_picker`; tests and
/// hosts with their own picker supply another.
abstract interface class ImageBackend {
  /// Picks up to [limit] images from the library, scaled to [maxDimension].
  Future<List<PickedEntry>> pick({
    required bool multiple,
    required int limit,
    int? maxDimension,
  });

  /// Takes a photo, scaled to [maxDimension]; null when cancelled.
  Future<PickedEntry?> capture({int? maxDimension});
}

/// Picks files. The default is `file_picker`.
abstract interface class FileBackend {
  /// Picks files of [type], restricted to [extensions] for
  /// [FileType.custom]; an empty list when cancelled.
  Future<List<PickedEntry>> pick({
    required FileType type,
    required List<String> extensions,
    required bool multiple,
  });
}

/// The media actions of Plux plugins: `pickImage`, `capturePhoto` and
/// `pickFile` (RT-060, SEC-080).
///
/// Register it in `PluxConfig.devicePackages`:
/// `PluxConfig(…, devicePackages: [PluxMedia()])`. Plugins must declare the
/// `photos`, `camera` or `files` device API, and the app must approve it.
final class PluxMedia implements PluxDevicePackage {
  /// Creates the package. [images] and [files] replace the default pickers.
  PluxMedia({
    @visibleForTesting ImageBackend? images,
    @visibleForTesting FileBackend? files,
  }) : _picker = _MediaPicker(
         images ?? _ImagePickerBackend(),
         files ?? const _FilePickerBackend(),
       );

  final _MediaPicker _picker;

  @override
  String get name => 'plux_media';

  @override
  Map<Type, Object> get services => {PluxMediaPicker: _picker};
}

final class _MediaPicker implements PluxMediaPicker {
  _MediaPicker(this._images, this._files);

  final ImageBackend _images;
  final FileBackend _files;

  @override
  Future<List<PluxPickedFile>> pickImages({
    required bool multiple,
    required int limit,
    int? maxDimension,
  }) => _guard(() async {
    final picked = await _images.pick(
      multiple: multiple,
      limit: limit,
      maxDimension: maxDimension,
    );
    return [for (final p in picked.take(limit)) _file(p, 'image/jpeg')];
  });

  @override
  Future<PluxPickedFile?> capturePhoto({int? maxDimension}) => _guard(() async {
    final p = await _images.capture(maxDimension: maxDimension);
    return p == null ? null : _file(p, 'image/jpeg');
  });

  @override
  Future<List<PluxPickedFile>> pickFiles({
    required List<String> mimeTypes,
    required bool multiple,
    required int limit,
  }) => _guard(() async {
    final (type, extensions) = pickerFilter(mimeTypes);
    final picked = await _files.pick(
      type: type,
      extensions: extensions,
      multiple: multiple,
    );
    return [
      for (final p in picked)
        if (mimeTypes.isEmpty || mimeAccepted(mimeOf(p), mimeTypes))
          _file(p, 'application/octet-stream'),
    ].take(limit).toList();
  });

  PluxPickedFile _file(PickedEntry p, String fallback) => PluxPickedFile(
    path: p.path,
    name: p.name,
    mimeType: p.mimeType ?? mimeForName(p.name) ?? fallback,
    sizeBytes: p.size,
  );
}

/// Turns a picker's platform failure into the runtime's typed one. The
/// message holds the platform's error code, never a path (SEC-092).
Future<T> _guard<T>(Future<T> Function() call) async {
  try {
    return await call();
  } on PlatformException catch (e) {
    if (e.code.endsWith('access_denied')) {
      throw PluxDeviceException.denied('the permission was denied: ${e.code}');
    }
    throw PluxDeviceException.unavailable('the picker failed: ${e.code}');
  }
}

/// Common media types by file extension.
const Map<String, String> _mimeByExtension = {
  'jpg': 'image/jpeg',
  'jpeg': 'image/jpeg',
  'png': 'image/png',
  'gif': 'image/gif',
  'webp': 'image/webp',
  'heic': 'image/heic',
  'avif': 'image/avif',
  'svg': 'image/svg+xml',
  'pdf': 'application/pdf',
  'txt': 'text/plain',
  'csv': 'text/csv',
  'json': 'application/json',
  'xml': 'application/xml',
  'zip': 'application/zip',
  'doc': 'application/msword',
  'docx':
      'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  'xls': 'application/vnd.ms-excel',
  'xlsx': 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
  'mp3': 'audio/mpeg',
  'wav': 'audio/wav',
  'm4a': 'audio/mp4',
  'mp4': 'video/mp4',
  'mov': 'video/quicktime',
  'webm': 'video/webm',
};

/// The media type of a file name by its extension, or null when unknown.
@visibleForTesting
String? mimeForName(String name) {
  final dot = name.lastIndexOf('.');
  if (dot < 0 || dot == name.length - 1) return null;
  return _mimeByExtension[name.substring(dot + 1).toLowerCase()];
}

/// The media type of a picked file.
@visibleForTesting
String mimeOf(PickedEntry p) =>
    p.mimeType ?? mimeForName(p.name) ?? 'application/octet-stream';

/// Whether [mime] is one of [accepted], which may use `type/*`.
@visibleForTesting
bool mimeAccepted(String mime, List<String> accepted) {
  for (final a in accepted) {
    if (a == mime) return true;
    if (a.endsWith('/*') && mime.startsWith(a.substring(0, a.length - 1))) {
      return true;
    }
  }
  return false;
}

/// The picker filter for [mimeTypes]: a platform file type, and the
/// extensions that restrict a custom one. Types the extension table does
/// not know open the picker for any file; the result is filtered by type
/// afterwards.
@visibleForTesting
(FileType, List<String>) pickerFilter(List<String> mimeTypes) {
  if (mimeTypes.isEmpty) return (FileType.any, const []);
  final wild = {
    for (final m in mimeTypes)
      if (m.endsWith('/*')) m.substring(0, m.length - 2),
  };
  if (wild.length == mimeTypes.length) {
    if (wild.length == 1) {
      switch (wild.single) {
        case 'image':
          return (FileType.image, const []);
        case 'video':
          return (FileType.video, const []);
        case 'audio':
          return (FileType.audio, const []);
      }
    }
    if (wild.length == 2 && wild.containsAll(const ['image', 'video'])) {
      return (FileType.media, const []);
    }
    return (FileType.any, const []);
  }
  final extensions = <String>{};
  for (final m in mimeTypes) {
    if (m.endsWith('/*')) return (FileType.any, const []);
    final found = [
      for (final e in _mimeByExtension.entries)
        if (e.value == m) e.key,
    ];
    if (found.isEmpty) return (FileType.any, const []);
    extensions.addAll(found);
  }
  return (FileType.custom, extensions.toList()..sort());
}

final class _ImagePickerBackend implements ImageBackend {
  _ImagePickerBackend() : _picker = ImagePicker();

  final ImagePicker _picker;

  @override
  Future<List<PickedEntry>> pick({
    required bool multiple,
    required int limit,
    int? maxDimension,
  }) async {
    final side = maxDimension?.toDouble();
    final List<XFile> files;
    if (multiple) {
      files = await _picker.pickMultiImage(
        maxWidth: side,
        maxHeight: side,
        limit: limit,
      );
    } else {
      final one = await _picker.pickImage(
        source: ImageSource.gallery,
        maxWidth: side,
        maxHeight: side,
      );
      files = [?one];
    }
    return [for (final f in files) await _entry(f)];
  }

  @override
  Future<PickedEntry?> capture({int? maxDimension}) async {
    final side = maxDimension?.toDouble();
    final f = await _picker.pickImage(
      source: ImageSource.camera,
      maxWidth: side,
      maxHeight: side,
    );
    return f == null ? null : _entry(f);
  }

  Future<PickedEntry> _entry(XFile f) async => (
    path: f.path,
    name: f.name,
    mimeType: f.mimeType,
    size: await f.length(),
  );
}

final class _FilePickerBackend implements FileBackend {
  const _FilePickerBackend();

  @override
  Future<List<PickedEntry>> pick({
    required FileType type,
    required List<String> extensions,
    required bool multiple,
  }) async {
    final allowed = type == FileType.custom ? extensions : null;
    final List<PlatformFile> files;
    if (multiple) {
      files = await FilePicker.pickFiles(
        type: type,
        allowedExtensions: allowed,
      );
    } else {
      final one = await FilePicker.pickFile(
        type: type,
        allowedExtensions: allowed,
      );
      files = [?one];
    }
    return [
      for (final f in files)
        if (f.path case final path?)
          (
            path: path,
            name: f.name,
            mimeType: null,
            size: await f.length() ?? 0,
          ),
    ];
  }
}
