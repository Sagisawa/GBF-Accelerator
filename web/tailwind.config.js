/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        canvas: '#f1f5f9',
        window: {
          bg: '#ffffff',
          border: '#cbd5e1',
          header: '#f8fafc',
        },
      },
      fontFamily: {
        sans: [
          '"Microsoft YaHei UI"',
          '-apple-system',
          'BlinkMacSystemFont',
          '"Segoe UI"',
          'Roboto',
          'Inter',
          'sans-serif',
        ],
        mono: [
          'Consolas',
          '"SF Mono"',
          'Menlo',
          'Monaco',
          'monospace',
        ],
      },
    },
  },
  plugins: [],
}

