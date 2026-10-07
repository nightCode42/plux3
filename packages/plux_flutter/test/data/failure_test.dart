// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/data/failure.dart';

void main() {
  test('an http failure reaches a step as its error with the status the '
      'server answered, which pages read as error.status [DAT-001]', () {
    const failure = DataFailure(
      ActionErrorKind.http,
      PluxErrorCode.dataHttpError,
      'login was answered with 401',
      status: 401,
    );
    final error = failure.toActionError();
    expect(error.kind, ActionErrorKind.http);
    expect(error.status, 401);
    expect(error.toPxl()['status'], 401);
  });

  test('a failure that no server answered has no status [DAT-001]', () {
    final error = const DataFailure.unavailable('no base URL').toActionError();
    expect(error.status, isNull);
    expect(error.toPxl()['status'], isNull);
  });
}
