// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The animation actions (ANI-002): `startAnimation` plays a timeline of
/// the page from the start; `controlAnimation` plays, pauses, reverses,
/// seeks or stops it. They reach the page's timelines through the
/// [PluxAnimations] service of the page's engine.
library;

import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/animation/registry.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/pxl/values.dart';

PluxAnimations _animations(StepContext c, String name) {
  final a = c.service<PluxAnimations>();
  if (a == null || a.run(name) == null) {
    throw ActionError(
      ActionErrorKind.validation,
      PluxErrorCode.animationUnknown,
      'the page has no timeline named $name',
    );
  }
  return a;
}

String _name(Map<String, Object?> i, String action) {
  final name = i['animation'];
  if (name is! String || name.isEmpty) {
    throw ActionError.validation('$action: animation is not a name');
  }
  return name;
}

/// The handlers of the animation actions, by action name.
final Map<String, ActionHandler> animationHandlers = {
  'startAnimation': FunctionHandler((c, i) {
    final name = _name(i, 'startAnimation');
    _animations(c, name).run(name)!.start();
    return const StepDone();
  }),
  'controlAnimation': FunctionHandler((c, i) {
    final name = _name(i, 'controlAnimation');
    final command = AnimationCommand.named(
      enumMember('AnimationCommand', i['command']),
    );
    if (command == null) {
      throw ActionError(
        ActionErrorKind.validation,
        PluxErrorCode.animationCommandInvalid,
        'controlAnimation: command is not play, pause, reverse, seek or stop',
      );
    }
    final at = i['position'];
    if (command == AnimationCommand.seek && at is! PxlDuration) {
      throw ActionError(
        ActionErrorKind.validation,
        PluxErrorCode.animationCommandInvalid,
        'controlAnimation: seek needs a position',
      );
    }
    _animations(c, name).control(
      name,
      command,
      at is PxlDuration ? Duration(milliseconds: at.millis) : null,
    );
    return const StepDone();
  }),
};
