package io.github.libxposed.api;

import android.content.pm.ApplicationInfo;

public abstract class XposedModule implements XposedModuleInterface {

    protected XposedModule() {
    }

    protected XposedModule(XposedInterface base, ModuleLoadedParam param) {
    }

    public ApplicationInfo getApplicationInfo() {
        return null;
    }

    public ApplicationInfo getModuleApplicationInfo() {
        return null;
    }

    public String getFrameworkName() {
        return "unknown";
    }

    public String getFrameworkVersion() {
        return "unknown";
    }

    public long getFrameworkVersionCode() {
        return -1L;
    }

    public int getApiVersion() {
        return 102;
    }

    @Override
    public void onModuleLoaded(ModuleLoadedParam param) {
    }

    @Override
    public void onPackageLoaded(PackageLoadedParam param) {
    }

    @Override
    public void onPackageReady(PackageReadyParam param) {
    }
}
