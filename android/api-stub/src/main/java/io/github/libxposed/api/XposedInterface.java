package io.github.libxposed.api;

import java.lang.reflect.Constructor;
import java.lang.reflect.Member;
import java.lang.reflect.Method;

public interface XposedInterface {

    interface Hooker {
    }

    interface BeforeHookCallback {
        Member getMember();
        Object getThisObject();
        Object[] getArgs();
        void returnAndSkip(Object result);
        void throwAndSkip(Throwable throwable);
    }

    interface AfterHookCallback {
        Member getMember();
        Object getThisObject();
        Object[] getArgs();
        Object getResult();
        Throwable getThrowable();
        boolean isSkipped();
        void setResult(Object result);
        void setThrowable(Throwable throwable);
    }

    interface MethodUnhooker {
        void unhook();
    }

    MethodUnhooker hook(Method method, Class<? extends Hooker> hookerClass);
    MethodUnhooker hook(Constructor<?> constructor, Class<? extends Hooker> hookerClass);
    MethodUnhooker hook(Method method, int priority, Class<? extends Hooker> hookerClass);
    MethodUnhooker hook(Constructor<?> constructor, int priority, Class<? extends Hooker> hookerClass);
    MethodUnhooker hookClassInitializer(Class<?> clazz, Class<? extends Hooker> hookerClass);
    MethodUnhooker hookClassInitializer(Class<?> clazz, int priority, Class<? extends Hooker> hookerClass);

    String getFrameworkName();
    String getFrameworkVersion();
    long getFrameworkVersionCode();
    int getApiVersion();
}
