import { createAsyncThunk, createSlice, type PayloadAction } from '@reduxjs/toolkit';

import { auth } from '../api/auth';
import type { AuthError, ChangePasswordData, Session, SignInData, User } from './types';

export interface AuthState {
  session: Session | null;
  status: 'idle' | 'loading' | 'authenticated' | 'unauthenticated';
  error: AuthError | null;
}

export interface SessionRootState {
  auth: AuthState;
}

const initialState: AuthState = {
  session: null,
  status: 'idle',
  error: null,
};

function toAuthError(err: unknown): AuthError {
  if (
    err &&
    typeof err === 'object' &&
    'code' in err &&
    'message' in err &&
    typeof (err as { message: unknown }).message === 'string'
  ) {
    return err as AuthError;
  }
  if (err instanceof Error) {
    return { code: 'unknown', message: err.message };
  }
  return { code: 'unknown', message: 'An unexpected error occurred.' };
}

export const bootstrapAuth = createAsyncThunk<Session | null, void, { rejectValue: AuthError }>(
  'auth/bootstrap',
  async (_, { rejectWithValue }) => {
    try {
      return await auth.getSession();
    } catch (err) {
      return rejectWithValue(toAuthError(err));
    }
  },
);

export const signIn = createAsyncThunk<Session, SignInData, { rejectValue: AuthError }>(
  'auth/signIn',
  async (data, { rejectWithValue }) => {
    try {
      return await auth.signIn(data);
    } catch (err) {
      return rejectWithValue(toAuthError(err));
    }
  },
);

export const changePassword = createAsyncThunk<
  void,
  ChangePasswordData,
  { rejectValue: AuthError }
>('auth/changePassword', async (data, { rejectWithValue }) => {
  try {
    await auth.changePassword(data);
  } catch (err) {
    return rejectWithValue(toAuthError(err));
  }
});

export const signOut = createAsyncThunk<void, void, { rejectValue: AuthError }>(
  'auth/signOut',
  async (_, { rejectWithValue }) => {
    try {
      await auth.signOut();
    } catch (err) {
      return rejectWithValue(toAuthError(err));
    }
  },
);

export const authSlice = createSlice({
  name: 'auth',
  initialState,
  reducers: {
    sessionChanged: (state, action: PayloadAction<Session | null>) => {
      state.session = action.payload;
      state.status = action.payload ? 'authenticated' : 'unauthenticated';
      state.error = null;
    },
    clearAuthError: (state) => {
      state.error = null;
    },
  },
  extraReducers: (builder) => {
    builder
      .addCase(bootstrapAuth.pending, (state) => {
        state.status = 'loading';
        state.error = null;
      })
      .addCase(bootstrapAuth.fulfilled, (state, action) => {
        state.session = action.payload;
        state.status = action.payload ? 'authenticated' : 'unauthenticated';
      })
      .addCase(bootstrapAuth.rejected, (state, action) => {
        state.session = null;
        state.status = 'unauthenticated';
        state.error = action.payload ?? { code: 'unknown', message: 'Failed to load session.' };
      })

      .addCase(signIn.pending, (state) => {
        state.status = 'loading';
        state.error = null;
      })
      .addCase(signIn.fulfilled, (state, action) => {
        state.session = action.payload;
        state.status = 'authenticated';
      })
      .addCase(signIn.rejected, (state, action) => {
        state.session = null;
        state.status = 'unauthenticated';
        state.error = action.payload ?? { code: 'unknown', message: 'Sign in failed.' };
      })

      .addCase(changePassword.rejected, (state, action) => {
        state.error = action.payload ?? { code: 'unknown', message: 'Password change failed.' };
      })

      .addCase(signOut.fulfilled, (state) => {
        state.session = null;
        state.status = 'unauthenticated';
        state.error = null;
      })
      .addCase(signOut.rejected, (state, action) => {
        state.session = null;
        state.status = 'unauthenticated';
        state.error = action.payload ?? null;
      });
  },
});

export const { sessionChanged, clearAuthError } = authSlice.actions;

export const selectSession = (state: SessionRootState): Session | null => state.auth.session;
export const selectUser = (state: SessionRootState): User | null =>
  state.auth.session?.user ?? null;
export const selectIsAuthenticated = (state: SessionRootState): boolean =>
  state.auth.status === 'authenticated';
export const selectAuthStatus = (state: SessionRootState): AuthState['status'] => state.auth.status;
export const selectAuthError = (state: SessionRootState): AuthError | null => state.auth.error;

export default authSlice.reducer;
