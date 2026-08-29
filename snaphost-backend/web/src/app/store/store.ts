import { configureStore } from '@reduxjs/toolkit';

import { authReducer } from '@/entities/session';

export const store = configureStore({
  reducer: {
    auth: authReducer,
  },
});
