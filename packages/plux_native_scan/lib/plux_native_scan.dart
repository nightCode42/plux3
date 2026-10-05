// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Builds a host app's native catalogue by static analysis (CLI-006,
/// WGT-030, ADR-0041), with no change to its code: the native routes, the
/// native slots and the custom actions plugins may use.
///
/// - **Routes** come from `GoRoute(name: …)` declarations, whose path
///   parameters are strings; from classes annotated with auto_route's
///   `@RoutePage`, whose `@PathParam` and `@QueryParam` constructor
///   parameters are their parameters (auto_route declares no result
///   type); and from `PluxConfig.nativeRoutes`, whose
///   `PluxNativeRoute<P, R>` takes its parameters from the constructor of
///   `P` and its result from `R`. A registration wins over a discovered
///   route of the same name, as at run time.
/// - **Slots** are the classes `plux.yaml` lists: the parameters of their
///   unnamed constructor are props, and callbacks named `on…` are events
///   with the callback's parameter as payload.
/// - **Actions** come from `PluxConfig.nativeActions`, whose
///   `PluxNativeAction<I, O>` takes its inputs from the constructor of `I`
///   and its output from `O`.
/// - **Packages** are the optional Plux packages the build registers in
///   `PluxConfig.devicePackages`, such as `plux_media`, named by the
///   package each entry's class comes from; publishing warns when a release
///   uses a device action whose package a build lacks (RT-060).
///
/// Dart types map to the document type system; one with no mapping is
/// reported with its source location and never guessed. An optional
/// parameter of such a type, or any prop, is left out; a route or action
/// that needs one is left out entirely, since plugins could not open or
/// call it. The catalogue is deterministic: entries sorted, keys sorted,
/// no timestamps and no absolute paths.
library;

import 'dart:convert';
import 'dart:io';

import 'package:analyzer/dart/analysis/analysis_context_collection.dart';
import 'package:analyzer/dart/analysis/results.dart';
import 'package:analyzer/dart/ast/ast.dart';
import 'package:analyzer/dart/ast/visitor.dart';
import 'package:analyzer/dart/element/element.dart';
import 'package:analyzer/dart/element/nullability_suffix.dart';
import 'package:analyzer/dart/element/type.dart';
import 'package:analyzer/source/line_info.dart';
import 'package:path/path.dart' as p;

/// The schema version of the catalogue documents this scanner writes.
const catalogueSchemaVersion = '1.0.0';

/// A problem the scan found, at a place in the host's code.
final class ScanProblem {
  /// Creates a problem.
  const ScanProblem(this.file, this.line, this.column, this.message);

  /// The file, relative to the host project's root, with forward slashes.
  final String file;

  /// The 1-based line and column.
  final int line, column;

  /// What is wrong, and what was left out.
  final String message;

  @override
  String toString() => '$file:$line:$column: $message';
}

/// The outcome of a scan: the catalogue document and the problems found.
final class ScanResult {
  /// Creates a result.
  const ScanResult(this.catalogue, this.problems);

  /// The native catalogue document.
  final Map<String, Object?> catalogue;

  /// The problems, sorted by file and position.
  final List<ScanProblem> problems;
}

/// Scans the Dart files under `lib/` of the host project at [root].
///
/// [slots] are the slot widget classes `plux.yaml` lists. [host] is the
/// host build, `<version>+<build>` as in `pubspec.yaml` (`1.4.0+52`); a
/// version without a build number has build 0. [id] is the catalogue's
/// UUIDv7, kept from the previous scan so the output stays the same;
/// [appId] optionally names the host app.
Future<ScanResult> scan({
  required String root,
  required List<String> slots,
  required String host,
  required String id,
  String? appId,
  String? sdkPath,
}) async {
  final dir = p.normalize(p.absolute(root));
  final lib = Directory(p.join(dir, 'lib'));
  final files = lib.existsSync()
      ? (lib
            .listSync(recursive: true)
            .whereType<File>()
            .where((f) => f.path.endsWith('.dart'))
            .map((f) => p.normalize(f.path))
            .toList()
          ..sort())
      : <String>[];
  final collection = AnalysisContextCollection(
    includedPaths: [dir],
    sdkPath: sdkPath == null ? null : p.normalize(p.absolute(sdkPath)),
  );
  final session = collection.contextFor(dir).currentSession;
  final s = _Scan(dir, slots.toSet());
  try {
    for (final f in files) {
      final unit = await session.getResolvedUnit(f);
      if (unit is ResolvedUnitResult) s.visit(unit);
    }
  } finally {
    await collection.dispose();
  }
  for (final missing in s.wantedSlots.difference(s.foundSlots)) {
    s.problems.add(
      ScanProblem(
        'plux.yaml',
        1,
        1,
        'no class $missing is declared under lib/',
      ),
    );
  }
  final (version, build) = _hostBuild(host);
  final routes = {...s.discovered, ...s.registered};
  final catalogue = <String, Object?>{
    'schemaVersion': catalogueSchemaVersion,
    'kind': 'nativeCatalogue',
    'id': id,
    'host': {'appId': ?appId, 'version': version, 'build': build},
    'routes': [for (final k in routes.keys.toList()..sort()) routes[k]],
    'slots': [for (final k in s.slots.keys.toList()..sort()) s.slots[k]],
    'actions': [for (final k in s.actions.keys.toList()..sort()) s.actions[k]],
    if (s.packages.isNotEmpty) 'packages': s.packages.toList()..sort(),
  };
  s.problems.sort(
    (a, b) => a.file != b.file
        ? a.file.compareTo(b.file)
        : a.line != b.line
        ? a.line - b.line
        : a.column - b.column,
  );
  return ScanResult(catalogue, s.problems);
}

/// The catalogue as the bytes `plux.catalogue.json` holds: keys sorted,
/// two-space indentation, a final newline.
String encodeCatalogue(Map<String, Object?> catalogue) =>
    '${const JsonEncoder.withIndent('  ').convert(_sorted(catalogue))}\n';

Object? _sorted(Object? v) => switch (v) {
  final Map<String, Object?> m => {
    for (final k in m.keys.toList()..sort()) k: _sorted(m[k]),
  },
  final List<Object?> l => [for (final e in l) _sorted(e)],
  _ => v,
};

(String, int) _hostBuild(String host) {
  final plus = host.indexOf('+');
  if (plus < 0) return (host, 0);
  return (host.substring(0, plus), int.tryParse(host.substring(plus + 1)) ?? 0);
}

final class _Scan {
  _Scan(this.root, this.wantedSlots);

  final String root;
  final Set<String> wantedSlots;
  final Set<String> foundSlots = {};
  final List<ScanProblem> problems = [];
  final Map<String, Map<String, Object?>> discovered = {};
  final Map<String, Map<String, Object?>> registered = {};
  final Map<String, Map<String, Object?>> slots = {};
  final Map<String, Map<String, Object?>> actions = {};
  final Set<String> packages = {};

  late String _file;
  late LineInfo _lines;

  void visit(ResolvedUnitResult unit) {
    _file = p.posix.joinAll(p.split(p.relative(unit.path, from: root)));
    _lines = unit.lineInfo;
    unit.unit.accept(_Visitor(this));
  }

  void report(AstNode at, String message) {
    final l = _lines.getLocation(at.offset);
    problems.add(ScanProblem(_file, l.lineNumber, l.columnNumber, message));
  }

  // ── Routes ───────────────────────────────────────────────────────────

  void goRoute(InstanceCreationExpression node) {
    final name = _stringArg(node, 'name');
    if (name == null) return; // only named routes are discovered
    final path = _stringArg(node, 'path') ?? '';
    discovered[name] = {
      'name': name,
      'params': [
        for (final m in RegExp(r':(\w+)').allMatches(path))
          {'name': m.group(1), 'type': 'string', 'required': true},
      ],
    };
  }

  void routePage(ClassDeclaration decl, ClassElement cls, ElementAnnotation a) {
    final value = a.computeConstantValue();
    final name =
        value?.getField('name')?.toStringValue() ??
        '${cls.name!.replaceFirst(RegExp(r'(Page|Screen)$'), '')}Route';
    final params = <Map<String, Object?>>[];
    final ctor = cls.unnamedConstructor;
    for (final f
        in ctor?.formalParameters ?? const <FormalParameterElement>[]) {
      if (f.name == 'key') continue;
      final ann = _pathOrQuery(f);
      if (ann == null) {
        if (f.isRequired) {
          report(
            decl,
            'route $name takes the argument ${f.name}, which a path cannot carry: register it in PluxConfig.nativeRoutes; left out',
          );
          return;
        }
        continue;
      }
      final type = _docType(f.type);
      if (type == null) {
        if (f.isRequired) {
          report(decl, 'route $name: ${_unmapped(f)}; left out');
          return;
        }
        report(decl, 'route $name: ${_unmapped(f)}; the parameter is left out');
        continue;
      }
      params.add({
        'name': ann.isEmpty ? f.name : ann,
        'type': type,
        if (_required(f)) 'required': true,
      });
    }
    final route = <String, Object?>{'name': name, 'params': params};
    discovered[name] = route;
  }

  void registrations(InstanceCreationExpression config) {
    for (final arg
        in config.argumentList.arguments.whereType<NamedArgument>()) {
      final which = arg.name.lexeme;
      if (which == 'devicePackages') {
        devicePackages(arg.argumentExpression);
        continue;
      }
      if (which != 'nativeRoutes' && which != 'nativeActions') continue;
      final map = arg.argumentExpression;
      if (map is! SetOrMapLiteral) {
        report(
          map,
          '$which is not a map literal, so it cannot be read; register its entries in place',
        );
        continue;
      }
      for (final e in map.elements) {
        if (e is! MapLiteralEntry || e.key is! SimpleStringLiteral) {
          report(
            e,
            'a $which entry whose key is not a string literal cannot be read',
          );
          continue;
        }
        final key = (e.key as SimpleStringLiteral).value;
        final type = e.value.staticType;
        if (type is! InterfaceType || type.typeArguments.length != 2) {
          report(e, '$which entry $key has no type arguments to read');
          continue;
        }
        final [input, output] = type.typeArguments;
        if (which == 'nativeRoutes') {
          final params = _classParams(e, 'route $key', input);
          final route = <String, Object?>{'name': key, 'params': ?params};
          if (params != null &&
              _output(e, 'route $key', output, route, 'result')) {
            registered[key] = route;
          }
        } else {
          final inputs = _classParams(e, 'custom action $key', input);
          final action = <String, Object?>{'name': key, 'inputs': ?inputs};
          if (inputs != null &&
              _output(e, 'custom action $key', output, action, 'output')) {
            actions[key] = action;
          }
        }
      }
    }
  }

  /// Records the packages of the entries of `devicePackages: [...]`.
  void devicePackages(Expression list) {
    if (list is! ListLiteral) {
      report(
        list,
        'devicePackages is not a list literal, so its packages cannot be read; list them in place',
      );
      return;
    }
    for (final e in list.elements) {
      final type = e is Expression ? e.staticType : null;
      final uri = type is InterfaceType
          ? type.element.library.uri.toString()
          : '';
      final name = _pluxPackage.firstMatch(uri)?.group(1);
      if (name == null || name == 'plux_flutter') {
        report(
          e,
          'a devicePackages entry that is not a class of a Plux package cannot be recorded',
        );
        continue;
      }
      packages.add(name);
    }
  }

  /// The parameters of [what] from the unnamed constructor of [type]; an
  /// empty list for `void`; null, reported, when they cannot be read.
  List<Map<String, Object?>>? _classParams(
    AstNode at,
    String what,
    DartType type,
  ) {
    if (_isVoid(type)) return [];
    final element = type is InterfaceType ? type.element : null;
    final ctor = element is ClassElement ? element.unnamedConstructor : null;
    if (ctor == null || _docType(type) != null) {
      report(
        at,
        '$what takes ${type.getDisplayString()}, which is not a class whose constructor names its parameters; left out',
      );
      return null;
    }
    final out = <Map<String, Object?>>[];
    for (final f in ctor.formalParameters) {
      final t = _docType(f.type);
      if (t == null) {
        if (_required(f)) {
          report(at, '$what: ${_unmapped(f)}; left out');
          return null;
        }
        report(at, '$what: ${_unmapped(f)}; the parameter is left out');
        continue;
      }
      out.add({'name': f.name, 'type': t, if (_required(f)) 'required': true});
    }
    return out;
  }

  bool _output(
    AstNode at,
    String what,
    DartType type,
    Map<String, Object?> into,
    String field,
  ) {
    if (_isVoid(type)) return true;
    final t = _docType(type);
    if (t == null) {
      report(
        at,
        '$what returns ${type.getDisplayString()}, which has no document type; left out',
      );
      return false;
    }
    into[field] = t;
    return true;
  }

  // ── Slots ────────────────────────────────────────────────────────────

  void slot(ClassDeclaration decl, ClassElement cls) {
    final name = cls.name!;
    foundSlots.add(name);
    final ctor = cls.unnamedConstructor;
    if (ctor == null) {
      report(
        decl,
        'slot $name has no unnamed constructor to read its props from; left out',
      );
      return;
    }
    final props = <Map<String, Object?>>[];
    final events = <Map<String, Object?>>[];
    for (final f in ctor.formalParameters) {
      if (f.name == 'key') continue;
      final type = f.type;
      final name = f.name ?? '';
      if (type is FunctionType) {
        if (!RegExp(r'^on[A-Z]').hasMatch(name) ||
            type.formalParameters.length > 1) {
          report(
            decl,
            'slot ${cls.name}: the callback $name is not an event (on…, with at most one parameter); left out',
          );
          continue;
        }
        if (type.formalParameters.isEmpty) {
          events.add({'name': name});
          continue;
        }
        final payload = _docType(type.formalParameters.single.type);
        if (payload == null) {
          report(
            decl,
            'slot ${cls.name}: the event $name carries ${type.formalParameters.single.type.getDisplayString()}, which has no document type; left out',
          );
          continue;
        }
        events.add({'name': name, 'payload': payload});
        continue;
      }
      final t = _docType(type);
      if (t == null) {
        report(
          decl,
          'slot ${cls.name}: ${_unmapped(f)}; the prop is left out, for the host\'s builder to supply',
        );
        continue;
      }
      props.add({'name': name, 'type': t, if (_required(f)) 'required': true});
    }
    slots[name] = {'type': name, 'props': props, 'events': events};
  }
}

final RegExp _pluxPackage = RegExp(r'^package:(plux_[a-z0-9_]+)/');

final class _Visitor extends RecursiveAstVisitor<void> {
  _Visitor(this.s);

  final _Scan s;

  @override
  void visitInstanceCreationExpression(InstanceCreationExpression node) {
    final cls = node.constructorName.element?.enclosingElement;
    final uri = cls?.library.uri.toString() ?? '';
    if (cls?.name == 'GoRoute' && uri.startsWith('package:go_router/')) {
      s.goRoute(node);
    } else if (cls?.name == 'PluxConfig' &&
        uri.startsWith('package:plux_flutter/')) {
      s.registrations(node);
    }
    super.visitInstanceCreationExpression(node);
  }

  @override
  void visitClassDeclaration(ClassDeclaration node) {
    final cls = node.declaredFragment?.element;
    if (cls != null) {
      for (final a in cls.metadata.annotations) {
        final e = a.element;
        final owner = e is ConstructorElement ? e.enclosingElement : null;
        if (owner?.name == 'RoutePage' &&
            owner!.library.uri.toString().startsWith('package:auto_route/')) {
          s.routePage(node, cls, a);
        }
      }
      if (s.wantedSlots.contains(cls.name)) s.slot(node, cls);
    }
    super.visitClassDeclaration(node);
  }
}

/// The name a `@PathParam` or `@QueryParam` gives a parameter, '' for its
/// own name; null without one.
String? _pathOrQuery(FormalParameterElement f) {
  for (final a in f.metadata.annotations) {
    final value = a.computeConstantValue();
    final cls = value?.type?.element;
    if (cls?.name == 'PathParam' || cls?.name == 'QueryParam') {
      return value?.getField('name')?.toStringValue() ?? '';
    }
  }
  return null;
}

String? _stringArg(InstanceCreationExpression node, String name) {
  for (final a in node.argumentList.arguments.whereType<NamedArgument>()) {
    if (a.name.lexeme == name && a.argumentExpression is SimpleStringLiteral) {
      return (a.argumentExpression as SimpleStringLiteral).value;
    }
  }
  return null;
}

bool _isVoid(DartType t) =>
    t is VoidType || t.isDartCoreNull || t.getDisplayString() == 'Null';

bool _required(FormalParameterElement f) =>
    f.isRequired && f.type.nullabilitySuffix != NullabilitySuffix.question;

String _unmapped(FormalParameterElement f) =>
    '${f.name} is a ${f.type.getDisplayString()}, which has no document type';

/// The document type expression (SCH-010) of a Dart type, or null.
String? _docType(DartType t) {
  final nullable = t.nullabilitySuffix == NullabilitySuffix.question;
  String? base;
  if (t.isDartCoreString) {
    base = 'string';
  } else if (t.isDartCoreInt) {
    base = 'int';
  } else if (t.isDartCoreDouble || t.isDartCoreNum) {
    base = 'double';
  } else if (t.isDartCoreBool) {
    base = 'bool';
  } else if (t is InterfaceType &&
      t.element.name == 'DateTime' &&
      t.element.library.isDartCore) {
    base = 'dateTime';
  } else if (t is InterfaceType && t.isDartCoreList) {
    final e = _docType(t.typeArguments.single);
    base = e == null ? null : 'list<$e>';
  } else if (t is InterfaceType &&
      t.isDartCoreMap &&
      t.typeArguments.first.isDartCoreString) {
    final v = _docType(t.typeArguments.last);
    base = v == null ? null : 'map<string,$v>';
  }
  if (base == null) return null;
  return nullable ? '$base?' : base;
}
