import { useDispatch, useSelector } from 'react-redux';
import type { ThunkDispatch, UnknownAction } from '@reduxjs/toolkit';

import type { SessionRootState } from './auth-slice';

type SessionDispatch = ThunkDispatch<SessionRootState, unknown, UnknownAction>;

export const useSessionDispatch = useDispatch.withTypes<SessionDispatch>();
export const useSessionSelector = useSelector.withTypes<SessionRootState>();
