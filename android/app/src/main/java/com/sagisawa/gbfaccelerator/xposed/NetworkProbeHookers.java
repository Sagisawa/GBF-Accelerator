package com.sagisawa.gbfaccelerator.xposed;

import android.webkit.WebResourceRequest;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import io.github.libxposed.api.XposedInterface;
import io.github.libxposed.api.annotations.BeforeInvocation;
import io.github.libxposed.api.annotations.XposedHooker;

public class NetworkProbeHookers {

    public interface InterceptListener {
        void onSetWebViewClient(WebView view, WebViewClient client);
        void onLoadUrl(WebView view, String url);
        void onShouldInterceptRequest(WebView view, WebResourceRequest request);
        void onOkHttpRequest(String method, String url);
        void onHttpUrlConnection(String method, String url);
    }

    private static volatile InterceptListener sListener;

    public static void setListener(InterceptListener listener) {
        sListener = listener;
    }

    @XposedHooker
    public static class SetWebViewClientHooker {
        @BeforeInvocation
        public static void before(XposedInterface.BeforeHookCallback cb) {
            InterceptListener l = sListener;
            if (l != null) {
                Object[] args = cb.getArgs();
                if (args != null && args.length > 0 && args[0] instanceof WebViewClient) {
                    l.onSetWebViewClient((WebView) cb.getThisObject(), (WebViewClient) args[0]);
                }
            }
        }
    }

    @XposedHooker
    public static class LoadUrlHooker {
        @BeforeInvocation
        public static void before(XposedInterface.BeforeHookCallback cb) {
            InterceptListener l = sListener;
            if (l != null) {
                Object[] args = cb.getArgs();
                if (args != null && args.length > 0 && args[0] instanceof String) {
                    l.onLoadUrl((WebView) cb.getThisObject(), (String) args[0]);
                }
            }
        }
    }

    @XposedHooker
    public static class ShouldInterceptRequestHooker {
        private static final ThreadLocal<Boolean> sInHook = new ThreadLocal<Boolean>() {
            @Override
            protected Boolean initialValue() {
                return Boolean.FALSE;
            }
        };

        @BeforeInvocation
        public static void before(XposedInterface.BeforeHookCallback cb) {
            if (Boolean.TRUE.equals(sInHook.get())) {
                return;
            }
            sInHook.set(Boolean.TRUE);
            try {
                InterceptListener l = sListener;
                if (l != null) {
                    Object[] args = cb.getArgs();
                    if (args != null && args.length >= 2 && args[1] instanceof WebResourceRequest) {
                        l.onShouldInterceptRequest((WebView) args[0], (WebResourceRequest) args[1]);
                    }
                }
            } finally {
                sInHook.set(Boolean.FALSE);
            }
        }
    }

    @XposedHooker
    public static class OkHttpExecuteHooker {
        @BeforeInvocation
        public static void before(XposedInterface.BeforeHookCallback cb) {
            InterceptListener l = sListener;
            if (l != null) {
                try {
                    Object call = cb.getThisObject();
                    java.lang.reflect.Method reqMethod = call.getClass().getMethod("request");
                    Object req = reqMethod.invoke(call);
                    if (req != null) {
                        java.lang.reflect.Method urlMethod = req.getClass().getMethod("url");
                        Object urlObj = urlMethod.invoke(req);
                        java.lang.reflect.Method methodMethod = req.getClass().getMethod("method");
                        String method = (String) methodMethod.invoke(req);
                        l.onOkHttpRequest(method, String.valueOf(urlObj));
                    }
                } catch (Throwable ignored) {}
            }
        }
    }

    @XposedHooker
    public static class OnReceivedSslErrorHooker {
        @BeforeInvocation
        public static void before(XposedInterface.BeforeHookCallback cb) {
            try {
                Object[] args = cb.getArgs();
                if (args != null && args.length >= 2 && args[1] instanceof android.webkit.SslErrorHandler) {
                    android.webkit.SslErrorHandler handler = (android.webkit.SslErrorHandler) args[1];
                    android.net.http.SslError error = (args.length >= 3 && args[2] instanceof android.net.http.SslError)
                        ? (android.net.http.SslError) args[2] : null;
                    String url = error != null ? error.getUrl() : "unknown";
                    android.util.Log.w("GBF-ACC", "[GBF-ACC][SSL] Caught SSL error for: " + url + " -> Calling proceed()!");
                    handler.proceed();
                    cb.returnAndSkip(null);
                }
            } catch (Throwable t) {
                android.util.Log.e("GBF-ACC", "[GBF-ACC][SSL] Error in onReceivedSslError handler", t);
            }
        }
    }

    @XposedHooker
    public static class VerifyServerCertificatesHooker {
        @BeforeInvocation
        public static void before(XposedInterface.BeforeHookCallback cb) {
            try {
                Object[] args = cb.getArgs();
                if (args != null && args.length >= 3 && args[2] instanceof String) {
                    String host = (String) args[2];
                    if (host.contains("granbluefantasy.jp") || host.contains("mbga.jp") || host.contains("akamaized.net")) {
                        android.util.Log.i("GBF-ACC", "[GBF-ACC][Chromium-SSL] Bypassing cert check for GBF host: " + host);
                        ClassLoader cl = cb.getMember().getDeclaringClass().getClassLoader();
                        Class<?> resultClass = cl.loadClass("org.chromium.net.AndroidCertVerifyResult");
                        java.lang.reflect.Constructor<?> ctor = resultClass.getConstructor(int.class);
                        Object okResult = ctor.newInstance(0); // 0 = VERIFY_OK
                        cb.returnAndSkip(okResult);
                    }
                }
            } catch (Throwable t) {
                android.util.Log.e("GBF-ACC", "[GBF-ACC][Chromium-SSL] Failed to return VERIFY_OK", t);
            }
        }
    }

    @XposedHooker
    public static class HttpURLConnectionHooker {
        @BeforeInvocation
        public static void before(XposedInterface.BeforeHookCallback cb) {
            InterceptListener l = sListener;
            if (l != null) {
                try {
                    Object conn = cb.getThisObject();
                    if (conn instanceof java.net.HttpURLConnection) {
                        java.net.HttpURLConnection http = (java.net.HttpURLConnection) conn;
                        l.onHttpUrlConnection(http.getRequestMethod(), http.getURL().toString());
                    }
                } catch (Throwable ignored) {}
            }
        }
    }
}
