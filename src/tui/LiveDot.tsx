import React, { useEffect, useState } from 'react';
import { Text } from 'ink';

interface LiveDotProps {
  /** Pulse (heartbeat) while true; steady when false. */
  pulsing: boolean;
  color: string;
}

/**
 * A small "live" indicator that pulses between ● and ◌ (~2 Hz) while active.
 *
 * It owns its own interval and is a leaf, so only this single character
 * repaints on each tick — never the whole status bar.
 */
export const LiveDot: React.FC<LiveDotProps> = ({ pulsing, color }) => {
  const [filled, setFilled] = useState(true);

  useEffect(() => {
    if (!pulsing) {
      setFilled(true);
      return;
    }
    const timer = setInterval(() => setFilled((v) => !v), 500);
    return () => clearInterval(timer);
  }, [pulsing]);

  return <Text color={color}>{filled ? '●' : '◌'}</Text>;
};
