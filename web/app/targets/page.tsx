import type { Metadata } from "next";
import { TargetsView } from "@/components/views/targets";

export const metadata: Metadata = { title: "Targets" };

export default function Page() {
  return <TargetsView />;
}
