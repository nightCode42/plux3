// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Extracts constructor signatures and enum values of Flutter declarations
/// from the pinned Flutter SDK, for the widget coverage table (WGT-003,
/// ADR-0010).
///
/// The targets are the Flutter counterparts named by the widget descriptors,
/// value types and enums under `schema/widgets/`. The snapshot is written to
/// `schema/widgets/flutter-api.json`; `schemagen` joins it with the registry
/// and fails when a parameter or enum value is neither supported nor
/// excluded.
library;

import 'dart:convert';
import 'dart:io';

import 'package:analyzer/dart/analysis/analysis_context_collection.dart';
import 'package:analyzer/dart/analysis/results.dart';
import 'package:analyzer/dart/element/element.dart';
import 'package:path/path.dart' as p;

/// A Flutter declaration to extract, identified by the public library that
/// exports it and its name.
sealed class Target {
  const Target(this.library, this.name);

  /// The library URI that exports the declaration, e.g.
  /// `package:flutter/material.dart`.
  final String library;

  /// The declaration name.
  final String name;

  /// The key of the declaration in the snapshot: `<library>#<name>`.
  String get key => '$library#$name';
}

/// A class and the constructors to extract, e.g. `FilledButton` with
/// `['', 'tonal']`.
final class ClassTarget extends Target {
  /// Creates a class target.
  const ClassTarget(super.library, super.name, this.constructors);

  /// Constructor names, sorted; the unnamed constructor is `''`.
  final List<String> constructors;
}

/// An enum whose values are extracted.
final class EnumTarget extends Target {
  /// Creates an enum target.
  const EnumTarget(super.library, super.name);
}

/// Reads the targets named by the registry files under [widgets]
/// (`schema/widgets/`), sorted by key.
///
/// Widget descriptors name one class in `flutter`, value types a list of
/// classes, and enums `{"library", "enum"}`. Constructors of a class named
/// more than once are merged. Files without a Flutter counterpart, such as
/// structural primitives and Plux enums, contribute nothing.
List<Target> readTargets(Directory widgets) {
  final classes = <String, ClassTarget>{};
  final enums = <String, EnumTarget>{};
  final files =
      widgets
          .listSync(recursive: true)
          .whereType<File>()
          .where((f) => f.path.endsWith('.json'))
          .where(
            (f) =>
                !f.path.endsWith('flutter-api.json') &&
                !f.path.endsWith('ids.lock.json'),
          )
          .toList()
        ..sort((a, b) => a.path.compareTo(b.path));
  for (final file in files) {
    final flutter = switch (jsonDecode(file.readAsStringSync())) {
      {'flutter': final Object f} => f,
      _ => null,
    };
    switch (flutter) {
      case {'library': final String library, 'enum': final String name}:
        final target = EnumTarget(library, name);
        enums[target.key] = target;
      case {'library': _, 'class': _}:
        _addClass(classes, flutter as Map<String, Object?>, file);
      case final List<Object?> list:
        for (final entry in list) {
          _addClass(classes, entry! as Map<String, Object?>, file);
        }
      case null:
        break;
      default:
        throw FormatException('unrecognised "flutter" entry', file.path);
    }
  }
  return <Target>[...classes.values, ...enums.values]
    ..sort((a, b) => a.key.compareTo(b.key));
}

void _addClass(
  Map<String, ClassTarget> classes,
  Map<String, Object?> entry,
  File file,
) {
  final (library, name, ctors) = switch (entry) {
    {
      'library': final String l,
      'class': final String c,
      'constructors': final List<Object?> k,
    } =>
      (l, c, k.cast<String>()),
    _ => throw FormatException(
      'a Flutter class needs library, class and constructors',
      file.path,
    ),
  };
  final key = '$library#$name';
  final merged = {...?classes[key]?.constructors, ...ctors}.toList()..sort();
  classes[key] = ClassTarget(library, name, merged);
}

/// Extracts the snapshot for [targets], resolving libraries in the analysis
/// context of [contextRoot], a package that depends on Flutter. [sdkPath]
/// is the Dart SDK to analyse with — the one bundled with Flutter; by
/// default the SDK running this code.
///
/// Parameters whose names start with `_` are private and skipped. Throws a
/// [StateError] listing every library, class, constructor or enum that does
/// not exist in the SDK, so a renamed or removed counterpart fails loudly.
Future<Map<String, Object?>> extract(
  List<Target> targets, {
  required String contextRoot,
  required String flutterVersion,
  String? sdkPath,
}) async {
  // The analyzer takes absolute paths in the platform's own form; a path
  // built by joining with "/" is not one on Windows.
  final root = p.normalize(p.absolute(contextRoot));
  final collection = AnalysisContextCollection(
    includedPaths: [root],
    sdkPath: sdkPath == null ? null : p.normalize(p.absolute(sdkPath)),
  );
  final session = collection.contextFor(root).currentSession;
  final classes = <String, Object?>{};
  final enums = <String, Object?>{};
  final errors = <String>[];
  try {
    for (final target in targets) {
      final result = await session.getLibraryByUri(target.library);
      if (result is! LibraryElementResult ||
          !File(result.element.firstFragment.source.fullName).existsSync()) {
        errors.add('cannot resolve ${target.library}');
        continue;
      }
      final element = result.element.exportNamespace.get2(target.name);
      switch (target) {
        case ClassTarget(:final constructors) when element is InterfaceElement:
          final missing = [
            for (final c in constructors)
              if (_constructor(element, c) == null) c,
          ];
          if (missing.isNotEmpty) {
            errors.add(
              '${target.key} has no constructors ${missing.map((c) => '"$c"').join(', ')}',
            );
            continue;
          }
          classes[target.key] = {
            'constructors': _constructors(element, constructors),
          };
        case ClassTarget():
          errors.add('${target.key} is not a class');
        case EnumTarget() when element is EnumElement:
          enums[target.key] = [
            for (final f in element.fields)
              if (f.isEnumConstant)
                {
                  'name': f.name,
                  if (f.metadata.hasDeprecated) 'deprecated': true,
                },
          ];
        case EnumTarget():
          errors.add('${target.key} is not an enum');
      }
    }
  } finally {
    await collection.dispose();
  }
  if (errors.isNotEmpty) throw StateError(errors.join('\n'));
  return {'flutter': flutterVersion, 'classes': classes, 'enums': enums};
}

Map<String, Object?> _constructors(
  InterfaceElement element,
  List<String> names,
) => {
  for (final name in names)
    name: [
      for (final p in _constructor(element, name)!.formalParameters)
        if (!(p.name ?? '').startsWith('_')) _parameter(p),
    ],
};

ConstructorElement? _constructor(InterfaceElement element, String name) {
  for (final c in element.constructors) {
    final cName = c.name ?? '';
    if ((cName == 'new' ? '' : cName) == name) return c;
  }
  return null;
}

Map<String, Object?> _parameter(FormalParameterElement p) => {
  'name': p.name,
  'type': p.type.getDisplayString(),
  'required': p.isRequired,
  'named': p.isNamed,
  if (p.defaultValueCode != null) 'default': p.defaultValueCode,
  if (p.metadata.hasDeprecated) 'deprecated': true,
};

/// Returns the root of the Flutter SDK the workspace at [root] resolves
/// `package:flutter` to, or `null` when it cannot be determined.
Directory? flutterSdk(Directory root) {
  // The path is normalised for the platform and has no trailing separator.
  final config = File(p.join(root.path, '.dart_tool', 'package_config.json'));
  if (!config.existsSync()) return null;
  final packages = switch (jsonDecode(config.readAsStringSync())) {
    {'packages': final List<Object?> list} => list,
    _ => const <Object?>[],
  };
  for (final package in packages) {
    if (package case {'name': 'flutter', 'rootUri': final String rootUri}) {
      final flutter = config.parent.uri.resolve(
        rootUri.endsWith('/') ? rootUri : '$rootUri/',
      );
      final sdk = Directory(
        p.normalize(Directory.fromUri(flutter.resolve('../../')).path),
      );
      return sdk.existsSync() ? sdk : null;
    }
  }
  return null;
}

/// Returns the version of the Flutter SDK the workspace at [root] resolves,
/// read from the SDK's `bin/cache/flutter.version.json`, or `null` when it
/// cannot be determined.
String? flutterSdkVersion(Directory root) {
  final sdk = flutterSdk(root);
  if (sdk == null) return null;
  final version = File(
    p.join(sdk.path, 'bin', 'cache', 'flutter.version.json'),
  );
  if (!version.existsSync()) return null;
  return switch (jsonDecode(version.readAsStringSync())) {
    {'frameworkVersion': final String v} => v,
    _ => null,
  };
}

/// Encodes the snapshot as indented JSON with a final newline. Map order is
/// the insertion order of [extract], which is sorted by target key.
String encode(Map<String, Object?> snapshot) =>
    '${const JsonEncoder.withIndent('  ').convert(snapshot)}\n';
