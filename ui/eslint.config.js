import js from "@eslint/js";
import pluginVue from "eslint-plugin-vue";
import pluginVueAccessibility from "eslint-plugin-vuejs-accessibility";
import globals from "globals";
import tseslint from "typescript-eslint";

export default tseslint.config(
  { ignores: ["**/node_modules/**", "**/.nuxt/**", "**/.output/**", "**/dist/**", "**/generated/**"] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...pluginVue.configs["flat/recommended"],
  ...pluginVueAccessibility.configs["flat/recommended"],
  {
    files: ["scripts/**/*.mjs"],
    languageOptions: { globals: globals.node }
  },
  {
    files: ["**/*.{ts,vue}"],
    languageOptions: {
      globals: { ...globals.browser, ...globals.node },
      parserOptions: { parser: tseslint.parser, extraFileExtensions: [".vue"] }
    },
    rules: {
      "vue/multi-word-component-names": "off",
      "vue/require-default-prop": "off",
      "vue/max-attributes-per-line": "off",
      "vue/singleline-html-element-content-newline": "off",
      "vue/html-self-closing": "off",
      "vuejs-accessibility/label-has-for": ["error", {
        required: { some: ["nesting", "id"] },
        allowChildren: true
      }]
    }
  }
);
