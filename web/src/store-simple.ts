import { create } from 'zustand';

interface SimpleStore {
  connectionStatus: string;
  connect: (url: string) => void;
  disconnect: () => void;
}

export const useSimpleStore = create<SimpleStore>((set) => ({
  connectionStatus: 'disconnected',
  
  connect: (url: string) => {
    console.log('Connecting to:', url);
    set({ connectionStatus: 'connected' });
  },
  
  disconnect: () => {
    set({ connectionStatus: 'disconnected' });
  },
}));

