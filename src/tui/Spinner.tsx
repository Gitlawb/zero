import React from 'react';
import { Text } from 'ink';
import Spinner from 'ink-spinner';
import { theme } from './theme';

interface ThinkingSpinnerProps {
  label?: string;
}

export const ThinkingSpinner: React.FC<ThinkingSpinnerProps> = ({ label = 'thinking' }) => {
  return (
    <Text color={theme.text.secondary}>
      <Spinner type="dots" /> {label}...
    </Text>
  );
};
