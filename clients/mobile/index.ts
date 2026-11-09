// crypto.getRandomValues must exist before @wuu/remote-core generates any
// key material (identity seeds, ephemerals) — the polyfill import has to be
// the first thing the bundle evaluates.
import "react-native-get-random-values";
import { registerRootComponent } from "expo";

import App from "./App";

registerRootComponent(App);
