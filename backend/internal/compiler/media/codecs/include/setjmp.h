// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/* Plux: WASI has no setjmp. Error recovery by longjmp becomes a trap,
 * which the host reports as a failed encode. */
#ifndef PLUX_SETJMP_H
#define PLUX_SETJMP_H
typedef int jmp_buf[1];
#define setjmp(env) 0
static inline __attribute__((noreturn)) void longjmp(jmp_buf env, int val) { (void)env; (void)val; __builtin_trap(); }
#endif
