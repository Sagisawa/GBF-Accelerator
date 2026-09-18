/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        background: '#090d16',
        surface: '#111827',
        'surface-card': '#1a2234',
        'surface-border': '#253248',
        brand: '#38bdf8',
        'brand-hover': '#0ea5e9',
        accent: '#818cf8',
      },
    },
  },
  plugins: [],
}
