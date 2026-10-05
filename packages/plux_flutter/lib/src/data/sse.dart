// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// A parser of `text/event-stream` (DAT-012): the server-sent events
/// format of the HTML living standard. Text arrives in chunks of any size;
/// lines end in LF, CR or CRLF; a blank line dispatches the event built
/// from its `event`, `data` and `id` fields (several `data` lines join
/// with LF); `retry` sets the reconnection delay; lines starting with a
/// colon are comments. The size of one event's data is bounded.
library;

/// One dispatched event.
final class SseEvent {
  /// Creates an event.
  const SseEvent({required this.event, required this.data, this.id});

  /// The event type; `message` when the stream names none.
  final String event;

  /// The data, its lines joined with LF.
  final String data;

  /// The last event ID in force when it was dispatched, or null.
  final String? id;
}

/// The data of one event is larger than the parser's bound.
final class SseTooLarge implements Exception {
  /// Creates the exception.
  const SseTooLarge();
}

/// Incremental `text/event-stream` parser.
final class SseParser {
  /// Creates a parser that refuses an event whose data exceeds
  /// [maxDataBytes] (counted in UTF-16 code units, an upper bound of the
  /// bytes the text had).
  SseParser({required this.maxDataBytes});

  /// The largest data of one event.
  final int maxDataBytes;

  /// The last `retry` value the stream sent, in milliseconds, or null.
  int? retryMs;

  /// The last event ID the stream sent; it persists across events and is
  /// what a reconnection sends as `Last-Event-ID`.
  String? lastEventId;

  final StringBuffer _line = StringBuffer();
  final List<String> _data = [];
  int _dataBytes = 0;
  String _event = '';
  bool _skipLf = false, _first = true;

  /// Feeds [chunk]; returns the events it completed. Throws [SseTooLarge]
  /// when an event's data passes the bound.
  List<SseEvent> add(String chunk) {
    final out = <SseEvent>[];
    var text = chunk;
    if (_first && text.isNotEmpty) {
      _first = false;
      if (text.codeUnitAt(0) == 0xFEFF) text = text.substring(1);
    }
    for (var i = 0; i < text.length; i++) {
      final c = text.codeUnitAt(i);
      if (_skipLf) {
        _skipLf = false;
        if (c == 0x0A) continue;
      }
      if (c == 0x0A || c == 0x0D) {
        _skipLf = c == 0x0D;
        final e = _endOfLine();
        if (e != null) out.add(e);
      } else {
        _line.writeCharCode(c);
        if (_line.length > maxDataBytes + 4096) throw const SseTooLarge();
      }
    }
    return out;
  }

  SseEvent? _endOfLine() {
    final line = _line.toString();
    _line.clear();
    if (line.isEmpty) return _dispatch();
    if (line.startsWith(':')) return null;
    final colon = line.indexOf(':');
    final field = colon < 0 ? line : line.substring(0, colon);
    var value = colon < 0 ? '' : line.substring(colon + 1);
    if (value.startsWith(' ')) value = value.substring(1);
    switch (field) {
      case 'event':
        _event = value;
      case 'data':
        _dataBytes += value.length + 1;
        if (_dataBytes > maxDataBytes) throw const SseTooLarge();
        _data.add(value);
      case 'id':
        if (!value.contains('\u0000')) lastEventId = value;
      case 'retry':
        if (value.isNotEmpty && RegExp(r'^[0-9]+$').hasMatch(value)) {
          retryMs = int.parse(value);
        }
    }
    return null;
  }

  SseEvent? _dispatch() {
    final event = _event;
    final data = _data;
    _event = '';
    _dataBytes = 0;
    if (data.isEmpty) return null;
    final out = SseEvent(
      event: event.isEmpty ? 'message' : event,
      data: data.join('\n'),
      id: lastEventId,
    );
    _data.clear();
    return out;
  }
}
