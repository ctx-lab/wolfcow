/* Minimal launcher targeting the libkrun v1.19.7 C API. */
#include <libkrun.h>
#include <stdio.h>
#include <string.h>
#include <sys/resource.h>
#include <dlfcn.h>
#include <errno.h>
#include <limits.h>
#include <mach-o/dyld.h>
#include <stdlib.h>
#include <unistd.h>
#ifdef WOLFCOW_SECURITY_PROBE
#include <fcntl.h>
#endif

static int check(int result, const char *stage) {
    if (result < 0) {
        fprintf(stderr, "wolfcow: %s: %s (%d)\n", stage, strerror(-result), result);
        return 0;
    }
    return 1;
}

static int parse_limit(const char *value, unsigned long low, unsigned long high, unsigned long *out) {
    char *end = NULL;
    errno = 0;
    unsigned long parsed = strtoul(value, &end, 10);
    if (errno != 0 || end == value || *end != '\0' || parsed < low || parsed > high) return 0;
    *out = parsed;
    return 1;
}

int main(int argc, char **argv) {
#ifdef WOLFCOW_SECURITY_PROBE
    if (argc == 3 && strcmp(argv[1], "--probe-read") == 0) {
        int fd = open(argv[2], O_RDONLY);
        if (fd < 0) {
            fprintf(stderr, "probe read denied: %s\n", strerror(errno));
            return 77;
        }
        close(fd);
        fprintf(stderr, "probe read allowed\n");
        return 0;
    }
#endif
    int agent_mode = argc == 6 && strcmp(argv[1], "--agent") == 0;
    if (argc != 2 && !agent_mode) {
        fprintf(stderr, "usage: wolfcow-krun ROOTFS | --agent ROOTFS SOCKET VCPUS MEMORY_MIB\n");
        return 1;
    }
    unsigned long cpus = 1, memory_mib = 512;
    if (agent_mode && (!parse_limit(argv[4], 1, 8, &cpus) ||
                       !parse_limit(argv[5], 128, 8192, &memory_mib))) {
        fprintf(stderr, "wolfcow: invalid VM resource bounds\n");
        return 1;
    }
    char executable[PATH_MAX], resolved[PATH_MAX];
    uint32_t length = sizeof(executable);
    if (_NSGetExecutablePath(executable, &length) != 0 ||
        realpath(executable, resolved) == NULL) {
        fprintf(stderr, "wolfcow: cannot locate runtime directory\n");
        return 1;
    }
    char *separator = strrchr(resolved, '/');
    if (separator == NULL) return 1;
    *separator = '\0';
    char firmware[PATH_MAX];
    if (snprintf(firmware, sizeof(firmware), "%s/libkrunfw.5.dylib", resolved) >= (int)sizeof(firmware)) return 1;
    // Load the bundled firmware explicitly before libkrun resolves its basename.
    if (dlopen(firmware, RTLD_NOW | RTLD_GLOBAL) == NULL) {
        fprintf(stderr, "wolfcow: load firmware: %s\n", dlerror());
        return 1;
    }
    struct rlimit limit;
    if (getrlimit(RLIMIT_NOFILE, &limit) == 0) {
        limit.rlim_cur = limit.rlim_max;
        if (setrlimit(RLIMIT_NOFILE, &limit) != 0) {
            perror("wolfcow: setrlimit");
            return 1;
        }
    }
    if (getenv("WOLFCOW_DEBUG") != NULL) {
        krun_set_log_level(KRUN_LOG_LEVEL_DEBUG);
    } else if (agent_mode) {
        krun_set_log_level(KRUN_LOG_LEVEL_WARN);
    }
    int id = krun_create_ctx();
    if (!check(id, "create VM")) return 1;
    const char *args[] = {NULL};
    const char *env[] = {"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
                         "HOME=/root", "TERM=vt100", NULL};
    if (!check(krun_set_vm_config(id, (uint8_t)cpus, (uint32_t)memory_mib), "configure VM") ||
        !check(krun_disable_implicit_vsock(id), "disable TSI/vsock") ||
        (agent_mode && (!check(krun_add_vsock(id, 0), "add vsock") ||
                        !check(krun_add_vsock_port2(id, 15000, argv[3], true), "map control port"))) ||
        !check(krun_set_root(id, agent_mode ? argv[2] : argv[1]), "configure rootfs") ||
        !check(krun_set_workdir(id, "/"), "configure working directory") ||
        !check(krun_set_exec(id, agent_mode ? "/wolfcow-guest" : "/bin/sh", args, env), "configure guest entrypoint")) {
        krun_free_ctx(id);
        return 1;
    }
    int result = krun_start_enter(id);
    int ok = check(result, "start VM");
    krun_free_ctx(id);
    return ok ? 0 : 1;
}
