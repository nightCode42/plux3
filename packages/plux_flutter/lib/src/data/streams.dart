// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The runtime's open streams (DAT-012, ADR-0048): where subscriptions
/// are made, counted against `data.streamsOpen` (LIM-004), told when the
/// host says the network is back, and closed with the scope that opened
/// them.
library;

import 'dart:async';
import 'dart:math';

import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/data/client.dart';
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/data/spec.dart';
import 'package:plux_flutter/src/data/stream_session.dart';
import 'package:plux_flutter/src/data/stream_transport.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/pxl/types.dart' show toJson;

/// Opens and counts streams.
final class DataStreams {
  /// Creates the streams over [transport]; [client] builds and checks
  /// each connection's request.
  DataStreams({
    required this.transport,
    required this.client,
    required this.limits,
    this.random,
    this.scheduler,
    bool Function()? networkAvailable,
  }) : _networkAvailable = networkAvailable ?? (() => true);

  /// Opens connections.
  final StreamTransport transport;

  /// Builds the requests.
  final DataClient client;

  /// The limits in force.
  final DataLimits Function() limits;

  /// The source of jitter; tests inject one.
  final Random? random;

  /// Starts the reconnection timers; tests inject one.
  final StreamScheduler? scheduler;

  final bool Function() _networkAvailable;
  final Set<StreamSession> _open = {};

  /// How many streams are open.
  int get openCount => _open.length;

  /// Subscribes to the stream of [spec] for [caller] with [params];
  /// refuses with `PLX-5112` when `data.streamsOpen` streams are open.
  /// Messages, status changes and the end are reported to the callbacks.
  StreamSession open({
    required DataSourceSpec spec,
    required DataCaller caller,
    required Map<String, Object?> params,
    required void Function(Object? message) onMessage,
    required void Function(StreamStatus status) onStatus,
    required void Function(DataFailure? failure) onEnd,
    void Function(DataFailure failure)? onProblem,
  }) {
    final l = limits();
    if (_open.length >= l.streamsOpen) {
      throw DataFailure(
        ActionErrorKind.custom,
        PluxErrorCode.dataStreamLimit,
        'stream ${spec.name} would be open stream number '
        '${_open.length + 1}, over data.streamsOpen = ${l.streamsOpen}',
      );
    }
    final graphql = spec.kind == DataKind.graphql;
    final Object? subscribe = graphql
        ? <String, Object?>{
            'query': spec.subscription,
            'variables': {
              for (final e in params.entries) e.key: toJson(e.value),
            },
          }
        : (spec.subscribeMessage == null
              ? null
              : toJson(spec.subscribeMessage));
    late final StreamSession session;
    session = StreamSession(
      transport: transport,
      open: (lastEventId) =>
          client.streamRequest(spec, caller, params, lastEventId: lastEventId),
      wire: graphql ? StreamWire.graphql : StreamWire.plain,
      subscribe: subscribe,
      minBackoff: l.streamBackoffMin,
      maxBackoff: l.streamBackoffMax,
      onMessage: onMessage,
      onStatus: onStatus,
      onEnd: (failure) {
        _open.remove(session);
        onEnd(failure);
      },
      onProblem: onProblem,
      refreshAuth: spec.auth ? client.refreshToken : null,
      random: random,
      scheduler: scheduler,
      networkAvailable: _networkAvailable,
    );
    _open.add(session);
    session.start();
    return session;
  }

  /// Closes [session].
  Future<void> close(StreamSession session) {
    _open.remove(session);
    return session.stop();
  }

  /// The host says the network is back: streams waiting to reconnect do
  /// so now.
  void networkBack() {
    for (final s in [..._open]) {
      s.nudge();
    }
  }

  /// Closes every stream.
  Future<void> closeAll() async {
    final all = [..._open];
    _open.clear();
    await Future.wait([for (final s in all) s.stop()]);
  }
}
