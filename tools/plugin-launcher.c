/* Trusted bootstrap, not plugin code. No shell, allocation or runtime threads
 * between closing inherited handles and exec. Fail closed on unsupported OSes. */
#define _GNU_SOURCE
#include <errno.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/resource.h>
#include <unistd.h>
#ifdef __linux__
#include <sys/syscall.h>
#endif
#ifdef __APPLE__
#include <spawn.h>
extern char **environ;
#endif
int main(int argc, char **argv) {
    if (argc < 2 || argv[1][0] != '/') return 64;
    struct rlimit core = {0, 0}, files = {64, 64};
    if (setrlimit(RLIMIT_CORE, &core) || setrlimit(RLIMIT_NOFILE, &files)) return 71;
#ifdef __APPLE__
    posix_spawnattr_t attr;
    posix_spawn_file_actions_t actions;
    if (posix_spawnattr_init(&attr) || posix_spawn_file_actions_init(&actions) ||
        posix_spawnattr_setflags(&attr, POSIX_SPAWN_CLOEXEC_DEFAULT | POSIX_SPAWN_SETEXEC)) return 71;
    for (int fd = 0; fd < 3; ++fd) {
        if (posix_spawn_file_actions_adddup2(&actions, fd, fd)) return 71;
    }
    pid_t pid;
    /* SETEXEC atomically replaces us; only explicit dup actions survive. */
    int err = posix_spawn(&pid, argv[1], &actions, &attr, argv + 1, environ);
    errno = err ? err : EIO;
#elif defined(__linux__) && defined(SYS_close_range)
    if (syscall(SYS_close_range, 3U, UINT_MAX, 0U) != 0) return 71;
    execv(argv[1], argv + 1);
#else
    return 69;
#endif
    perror("plugin sandbox exec");
    return 71;
}
