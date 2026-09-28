// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/* Plux: WASI builds are single-threaded; pthread_once runs inline. */
#ifndef PLUX_PTHREAD_H
#define PLUX_PTHREAD_H
typedef int pthread_once_t;
#define PTHREAD_ONCE_INIT 0
static inline int pthread_once(pthread_once_t* once, void (*fn)(void)) {
  if (!*once) { *once = 1; fn(); }
  return 0;
}
typedef int pthread_t;
static inline int pthread_create(pthread_t* t, const void* attr, void* (*fn)(void*), void* arg) {
  (void)t; (void)attr; (void)fn; (void)arg;
  return -1; /* no threads: callers fall back to one */
}
static inline int pthread_join(pthread_t t, void** out) { (void)t; (void)out; return -1; }
#endif
