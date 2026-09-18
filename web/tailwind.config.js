/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        canvas: '#0D0E11',
        surface: {
          subtle: '#121418',
          DEFAULT: '#16181D',
          elevated: '#1E2128',
          active: '#282C36',
        },
        hairline: {
          subtle: 'rgba(255, 255, 255, 0.05)',
          DEFAULT: 'rgba(255, 255, 255, 0.08)',
          strong: 'rgba(255, 255, 255, 0.14)',
          focus: 'rgba(255, 255, 255, 0.25)',
        },
        label: {
          primary: '#F0F1F3',
          secondary: '#9094A0',
          tertiary: '#5D616F',
          disabled: '#3B3E48',
        },
        sys: {
          green: '#30D158',
          greenBg: 'rgba(48, 209, 88, 0.12)',
          blue: '#0A84FF',
          blueBg: 'rgba(10, 132, 255, 0.12)',
          amber: '#FF9F0A',
          amberBg: 'rgba(255, 159, 10, 0.12)',
          red: '#FF453A',
          redBg: 'rgba(255, 69, 58, 0.12)',
        },
      },
      boxShadow: {
        specular: 'inset 0 1px 0 0 rgba(255, 255, 255, 0.06), 0 1px 2px 0 rgba(0, 0, 0, 0.5)',
        'specular-active': 'inset 0 1px 1px 0 rgba(0, 0, 0, 0.4), 0 1px 0 0 rgba(255, 255, 255, 0.04)',
      },
      fontFamily: {
        sans: [
          '-apple-system',
          'BlinkMacSystemFont',
          '"SF Pro Text"',
          '"SF Pro Display"',
          '"Inter"',
          '"Segoe UI"',
          'Roboto',
          'sans-serif',
        ],
        mono: [
          '"SF Mono"',
          'Menlo',
          'Monaco',
          'Consolas',
          '"Liberation Mono"',
          '"Courier New"',
          'monospace',
        ],
      },
    },
  },
  plugins: [],
}
