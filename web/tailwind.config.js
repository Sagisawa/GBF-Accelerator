/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        canvas: '#000000',
        surface: {
          subtle: '#121214',
          card: '#161618',
          DEFAULT: '#1C1C1E',
          grouped: '#1C1C1E',
          elevated: '#2C2C2E',
          active: '#3A3A3C',
        },
        hairline: {
          subtle: 'rgba(255, 255, 255, 0.04)',
          DEFAULT: 'rgba(255, 255, 255, 0.08)',
          strong: 'rgba(255, 255, 255, 0.16)',
          focus: 'rgba(255, 255, 255, 0.3)',
        },
        label: {
          primary: '#FFFFFF',
          secondary: '#8E8E93',
          tertiary: '#636366',
          quaternary: '#48484A',
          disabled: '#3A3A3C',
        },
        apple: {
          red: '#FA2D55',
          redHover: '#E02448',
          redBg: 'rgba(250, 45, 85, 0.12)',
          redGlow: 'rgba(250, 45, 85, 0.25)',
          green: '#34C759',
          greenBg: 'rgba(52, 199, 89, 0.12)',
          blue: '#0071E3',
          blueBg: 'rgba(0, 113, 227, 0.12)',
          amber: '#FF9F0A',
          amberBg: 'rgba(255, 159, 10, 0.12)',
        },
        sys: {
          green: '#34C759',
          greenBg: 'rgba(52, 199, 89, 0.12)',
          blue: '#0071E3',
          blueBg: 'rgba(0, 113, 227, 0.12)',
          amber: '#FF9F0A',
          amberBg: 'rgba(255, 159, 10, 0.12)',
          red: '#FA2D55',
          redBg: 'rgba(250, 45, 85, 0.12)',
        },
      },
      boxShadow: {
        apple: '0 4px 20px -2px rgba(0, 0, 0, 0.5), 0 0 0 1px rgba(255, 255, 255, 0.08)',
        'apple-pop': '0 12px 36px -4px rgba(0, 0, 0, 0.7), 0 0 0 1px rgba(255, 255, 255, 0.12)',
        specular: 'inset 0 1px 0 0 rgba(255, 255, 255, 0.08), 0 1px 2px 0 rgba(0, 0, 0, 0.5)',
        'specular-active': 'inset 0 1px 1px 0 rgba(0, 0, 0, 0.4), 0 1px 0 0 rgba(255, 255, 255, 0.04)',
      },
      fontFamily: {
        sans: [
          '-apple-system',
          'BlinkMacSystemFont',
          '"SF Pro Display"',
          '"SF Pro Text"',
          '"Helvetica Neue"',
          'Inter',
          'sans-serif',
        ],
        mono: [
          '"SF Mono"',
          'Menlo',
          'Monaco',
          'Consolas',
          'monospace',
        ],
      },
    },
  },
  plugins: [],
}
