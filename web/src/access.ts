import { createContext, useContext } from "react";
export const OperationAccess = createContext(true);
export const useCanOperate = () => useContext(OperationAccess);

export const OwnerAccess = createContext(false);
export const useIsOwner = () => useContext(OwnerAccess);
