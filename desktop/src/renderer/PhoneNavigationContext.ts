import { createContext } from 'react';

/** The phone shell owns account navigation outside the connected workbench. */
export const PhoneNavigationContext = createContext<{
  computer?: string;
  openDevices: () => void;
} | null>(null);
