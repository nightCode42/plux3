// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import { docsSchema } from "@astrojs/starlight/schema";
import { defineCollection } from "astro:content";
import { pluxDocs } from "./loader";

export const collections = {
  docs: defineCollection({ loader: pluxDocs(), schema: docsSchema() }),
};
