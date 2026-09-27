package io.github.libxposed.api;

public interface XposedModuleInterface {

    interface ModuleLoadedParam {
        String getProcessName();
    }

    interface PackageLoadedParam {
        String getPackageName();
        ClassLoader getClassLoader();
        boolean isFirstPackage();
    }

    interface PackageReadyParam {
        String getPackageName();
        ClassLoader getClassLoader();
    }

    void onModuleLoaded(ModuleLoadedParam param);
    void onPackageLoaded(PackageLoadedParam param);
    void onPackageReady(PackageReadyParam param);
}
