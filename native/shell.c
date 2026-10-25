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
#ifdef CTX_SECURITY_PROBE
#include <fcntl.h>
#endif

static int check(int result, const char *stage) {
    if (result < 0) {
        fprintf(stderr, "ctx: %s: %s (%d)\n", stage, strerror(-result), result);
        return 0;
    }
    return 1;
}

int main(int argc, char **argv) {
#ifdef CTX_SECURITY_PROBE
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
    if (argc != 2) {
        fprintf(stderr, "usage: ctx-krun ROOTFS\n");
        return 1;
    }
    char executable[PATH_MAX], resolved[PATH_MAX];
    uint32_t length = sizeof(executable);
    if (_NSGetExecutablePath(executable, &length) != 0 ||
        realpath(executable, resolved) == NULL) {
        fprintf(stderr, "ctx: cannot locate runtime directory\n");
        return 1;
    }
    char *separator = strrchr(resolved, '/');
    if (separator == NULL) return 1;
    *separator = '\0';
    char firmware[PATH_MAX];
    if (snprintf(firmware, sizeof(firmware), "%s/libkrunfw.5.dylib", resolved) >= (int)sizeof(firmware)) return 1;
    // Load the bundled firmware explicitly before libkrun resolves its basename.
    if (dlopen(firmware, RTLD_NOW | RTLD_GLOBAL) == NULL) {
        fprintf(stderr, "ctx: load firmware: %s\n", dlerror());
        return 1;
    }
    struct rlimit limit;
    if (getrlimit(RLIMIT_NOFILE, &limit) == 0) {
        limit.rlim_cur = limit.rlim_max;
        if (setrlimit(RLIMIT_NOFILE, &limit) != 0) {
            perror("ctx: setrlimit");
            return 1;
        }
    }
    int id = krun_create_ctx();
    if (!check(id, "create VM")) return 1;
    const char *args[] = {NULL};
    const char *env[] = {"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
                         "HOME=/root", "TERM=vt100", NULL};
    if (!check(krun_set_vm_config(id, 1, 512), "configure VM") ||
        !check(krun_disable_implicit_vsock(id), "disable TSI/vsock") ||
        !check(krun_set_root(id, argv[1]), "configure rootfs") ||
        !check(krun_set_workdir(id, "/"), "configure working directory") ||
        !check(krun_set_exec(id, "/bin/sh", args, env), "configure shell")) {
        krun_free_ctx(id);
        return 1;
    }
    int result = krun_start_enter(id);
    check(result, "start VM");
    krun_free_ctx(id);
    return 1;
}
