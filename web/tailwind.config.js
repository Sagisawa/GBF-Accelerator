/** @type {import('tailwindcss').Config} */
export default {
  darkMode: 'class',
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        canvas: 'var(--bg-canvas)',
        window: {
          bg: '#ffffff',
          border: '#cbd5e1',
          header: '#f8fafc',
        },
        // Semantic system tokens (consumed by Badge and shared primitives)
        sys: {
          green: 'var(--sys-green)',
          greenBg: 'var(--sys-green-bg)',
          blue: 'var(--sys-blue)',
          blueBg: 'var(--sys-blue-bg)',
          amber: 'var(--sys-amber)',
          amberBg: 'var(--sys-amber-bg)',
          red: 'var(--sys-red)',
          redBg: 'var(--sys-red-bg)',
        },
        surface: {
          active: 'var(--control-bg)',
        },
        label: {
          primary: 'var(--text-primary)',
          secondary: 'var(--text-secondary)',
          tertiary: 'var(--text-muted)',
        },
        hairline: 'var(--hairline)',
      },
      borderColor: {
        hairline: 'var(--hairline)',
      },
      spacing: {
        4.5: '1.125rem',
        5.5: '1.375rem',
        18: '4.5rem',
      },
      boxShadow: {
        '2xs': '0 1px 0 0 rgba(15, 23, 42, 0.04)',
        xs: '0 1px 2px 0 rgba(15, 23, 42, 0.06)',
      },
      fontFamily: {
        sans: [
          '-apple-system',
          'BlinkMacSystemFont',
          '"Segoe UI Variable Text"',
          '"Segoe UI"',
          'Inter',
          '"PingFang SC"',
          '"Hiragino Sans"',
          '"Microsoft YaHei UI"',
          '"Yu Gothic UI"',
          'Roboto',
          'sans-serif',
        ],
        mono: [
          'ui-monospace',
          '"SF Mono"',
          '"Cascadia Mono"',
          'Menlo',
          'Consolas',
          'monospace',
        ],
      },
      transitionTimingFunction: {
        fluid: 'cubic-bezier(0.22, 1, 0.36, 1)',
      },
      keyframes: {
        'ui-fade': {
          from: { opacity: '0' },
          to: { opacity: '1' },
        },
        'ui-modal': {
          from: { opacity: '0', transform: 'translate3d(0, 6px, 0) scale(0.97)' },
          to: { opacity: '1', transform: 'translate3d(0, 0, 0) scale(1)' },
        },
        'ui-toast': {
          from: { opacity: '0', transform: 'translate3d(0, -8px, 0) scale(0.98)' },
          to: { opacity: '1', transform: 'translate3d(0, 0, 0) scale(1)' },
        },
        'ui-drawer': {
          from: { transform: 'translate3d(0, 24px, 0)', opacity: '0' },
          to: { transform: 'translate3d(0, 0, 0)', opacity: '1' },
        },
      },
      animation: {
        'fade-soft': 'ui-fade 180ms cubic-bezier(0.22, 1, 0.36, 1) both',
        'modal-in': 'ui-modal 220ms cubic-bezier(0.22, 1, 0.36, 1) both',
        'toast-in': 'ui-toast 240ms cubic-bezier(0.22, 1, 0.36, 1) both',
        'drawer-in': 'ui-drawer 260ms cubic-bezier(0.22, 1, 0.36, 1) both',
      },
    },
  },
  plugins: [],
}
