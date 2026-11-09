import { createRoot } from "react-dom/client";
import { TemporaryPlayground } from "./TemporaryPlayground";
import "./index.css";
import "blobatar/motion.css";

createRoot(document.getElementById("root")!).render(<TemporaryPlayground />);
