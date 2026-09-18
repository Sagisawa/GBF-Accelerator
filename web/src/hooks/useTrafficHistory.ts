import { useState, useEffect, useRef } from 'react'

export interface TrafficPoint {
  time: number
  qps: number
}

export function useTrafficHistory(totalRequests: number, windowSize: number = 30) {
  const [history, setHistory] = useState<number[]>(() => new Array(windowSize).fill(0))
  const lastTotalRef = useRef<number>(totalRequests)
  const lastTimeRef = useRef<number>(Date.now())

  useEffect(() => {
    const interval = setInterval(() => {
      const now = Date.now()
      const dt = Math.max((now - lastTimeRef.current) / 1000, 0.5)
      const delta = Math.max(totalRequests - lastTotalRef.current, 0)
      const currentQps = Math.round((delta / dt) * 10) / 10

      lastTotalRef.current = totalRequests
      lastTimeRef.current = now

      setHistory((prev) => {
        const next = [...prev.slice(1), currentQps]
        return next
      })
    }, 1000)

    return () => clearInterval(interval)
  }, [totalRequests, windowSize])

  // Generate SVG polyline coordinates normalized to width and height
  const getPolylinePoints = (width: number = 120, height: number = 28) => {
    const maxVal = Math.max(...history, 5) // at least scale of 5 to avoid flat 0 division
    const stepX = width / (windowSize - 1)

    return history
      .map((val, idx) => {
        const x = idx * stepX
        const y = height - (val / maxVal) * (height - 4) - 2
        return `${x.toFixed(1)},${y.toFixed(1)}`
      })
      .join(' ')
  }

  const currentQps = history[history.length - 1] || 0

  return {
    history,
    currentQps,
    getPolylinePoints,
  }
}
