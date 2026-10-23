import type { Metadata } from "next";
import { KeysView } from "@/components/views/keys";

export const metadata: Metadata = { title: "API keys" };

export default function Page() {
  return <KeysView />;
}
